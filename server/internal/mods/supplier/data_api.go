package supplier

// T3/T4 对外供货 API 实现（本站作上游，zcard-supply-v2 协议）。
//
// 下单链路（与前台共用同一库存池——防超卖）：
//   downstream_order_no 幂等 → 供货价核算（覆盖价 > 基础价）→ 账本扣款
//   （幂等键 supply_order:<downID>）→ inventory.Reserve 锁卡 → MarkUsed 交付
//   → 解密卡密（内存态）→ 响应 fulfillment.delivered + cards → 回调转发登记
//
// 余额不足/库存不足 → 明确错误码（下游可编程处理），不产生流水。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"log/slog"

	"google.golang.org/protobuf/types/known/emptypb"
)

// SupplyAPIService 对外供货协议实现（替换 M0 的 Ping 占位）。
type SupplyAPIService struct {
	supplyv1.UnimplementedSupplyServiceServer
	repo   *SupplierRepoImpl
	reader catalogport.SupplierCatalog
	inv    invport.Inventory
	cards  invport.CardContentReader // 交付卡密读取（内存态解密）
	enq    queue.Enqueuer            // 回调转发调度
	gen    *id.Generator
	log    *slog.Logger
}

// NewSupplyAPIService 构造。
func NewSupplyAPIService(repo *SupplierRepoImpl, reader catalogport.SupplierCatalog, inv invport.Inventory, cards invport.CardContentReader, enq queue.Enqueuer, gen *id.Generator, logger *slog.Logger) *SupplyAPIService {
	return &SupplyAPIService{repo: repo, reader: reader, inv: inv, cards: cards, enq: enq, gen: gen, log: logger}
}

// ServerVersion 版本注入。
var ServerVersion = "dev"

// Ping 连通性（免签名；鉴权后返回余额）。
func (s *SupplyAPIService) Ping(ctx context.Context, _ *emptypb.Empty) (*supplyv1.PingReply, error) {
	reply := &supplyv1.PingReply{
		Protocol:   "zcard-supply-v2",
		Version:    ServerVersion,
		ServerTime: time.Now().Unix(),
		Ok:         true,
	}
	if accountID := SupplyAccountID(ctx); accountID > 0 {
		if balance, err := s.repo.BalanceOf(ctx, accountID); err == nil {
			reply.Balance = balance
			reply.Currency = "CNY"
		}
	}
	return reply, nil
}

// ListCategories 分类（下游建目录用；扁平分类树由商品侧推断）。
func (s *SupplyAPIService) ListCategories(ctx context.Context, _ *emptypb.Empty) (*supplyv1.ListCategoriesReply, error) {
	return &supplyv1.ListCategoriesReply{Categories: []*supplyv1.SupplyCategory{}}, nil
}

// ListProducts 商品列表（供货价口径 + include_inactive）。
func (s *SupplyAPIService) ListProducts(ctx context.Context, req *supplyv1.ListProductsRequest) (*supplyv1.ListProductsReply, error) {
	accountID := SupplyAccountID(ctx)
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	status := int8(1)
	if req.GetIncludeInactive() {
		status = -1 // 含下架
	}
	items, total, err := s.reader.ListForSupply(ctx, catalogport.AdminFilter{Status: status, Page: int32(page), PageSize: int32(pageSize)})
	if err != nil {
		return nil, err
	}
	reply := &supplyv1.ListProductsReply{
		Total:    total,
		PageSize: int32(pageSize),
		HasMore:  int64(page)*int64(pageSize) < total,
	}
	for _, p := range items {
		price := p.Price
		if override, err := s.repo.PriceOf(ctx, accountID, p.ID, 0); err == nil && override > 0 {
			price = override
		}
		reply.Items = append(reply.Items, &supplyv1.SupplyProduct{
			Id:           strconv.FormatUint(p.ID, 10),
			Name:         p.Name,
			Price:        price,
			FactoryPrice: p.FactoryPrice,
			CategoryId:   fmt.Sprint(p.CategoryID),
			Description:  p.Description,
			Cover:        p.Cover,
			IsActive:     p.Status == 1,
		})
	}
	return reply, nil
}

// GetProduct 商品详情。
func (s *SupplyAPIService) GetProduct(ctx context.Context, req *supplyv1.GetProductRequest) (*supplyv1.GetProductReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_PRODUCT_ID")
	}
	accountID := SupplyAccountID(ctx)
	row, err := s.reader.GetForSupply(ctx, id)
	if err != nil {
		return nil, errors.New("supplier.PRODUCT_NOT_FOUND")
	}
	price := row.Price
	if override, err := s.repo.PriceOf(ctx, accountID, row.ID, 0); err == nil && override > 0 {
		price = override
	}
	return &supplyv1.GetProductReply{Product: &supplyv1.SupplyProduct{
		Id: strconv.FormatUint(row.ID, 10), Name: row.Name, Price: price,
		FactoryPrice: row.FactoryPrice, IsActive: row.Status == 1,
	}}, nil
}

// GetStock 实时库存。
func (s *SupplyAPIService) GetStock(ctx context.Context, req *supplyv1.GetStockRequest) (*supplyv1.GetStockReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_PRODUCT_ID")
	}
	stock, err := s.inv.Stock(ctx, id, 0)
	if err != nil {
		return nil, err
	}
	return &supplyv1.GetStockReply{Stock: int32(stock)}, nil
}

// CreateOrder 下单：幂等 → 核算 → 扣款 → 锁卡 → 交付。
func (s *SupplyAPIService) CreateOrder(ctx context.Context, req *supplyv1.CreateSupplyOrderRequest) (*supplyv1.CreateSupplyOrderReply, error) {
	accountID := SupplyAccountID(ctx)
	if req.GetDownstreamOrderNo() == "" {
		return nil, errors.New("supplier.DOWNSTREAM_ORDER_NO_REQUIRED")
	}
	if req.GetQuantity() < 1 {
		return nil, errors.New("supplier.INVALID_QUANTITY")
	}
	// 幂等：同 downstream_order_no 重复下单返回首单
	if existing, err := s.repo.GetSupplyOrderByNo(ctx, req.GetDownstreamOrderNo()); err == nil {
		return s.orderReply(existing), nil
	}
	productID, err := strconv.ParseUint(req.GetProductId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_PRODUCT_ID")
	}
	p, err := s.reader.GetForSupply(ctx, productID)
	if err != nil || p.Status == 0 {
		return &supplyv1.CreateSupplyOrderReply{Status: "rejected", ErrorCode: "product_unavailable", ErrorMessage: "商品不可用"}, nil
	}
	// 供货价（覆盖价 > 基础价）
	price := p.Price
	if override, err := s.repo.PriceOf(ctx, accountID, productID, 0); err == nil && override > 0 {
		price = override
	}
	amount := price * int64(req.GetQuantity())
	// 建单（pending）
	items := []map[string]any{{
		"product_id": productID, "name": p.Name, "quantity": req.GetQuantity(), "unit_price": price,
	}}
	order, err := s.repo.CreateSupplyOrder(ctx, accountID, req.GetDownstreamOrderNo(), items, amount)
	if err != nil {
		return nil, err
	}
	// 账本扣款（幂等键 supply_order:<downID>；余额不足不产生流水）
	ref := "supply_order:" + req.GetDownstreamOrderNo()
	if err := s.repo.LedgerEntry(ctx, accountID, order.ID, "supply_pay", -amount, ref, "下游下单扣款"); err != nil {
		_ = s.repo.MarkSupplyOrderRejected(ctx, order.ID)
		if errors.Is(err, ErrInsufficientBalance) {
			return &supplyv1.CreateSupplyOrderReply{Status: "rejected", ErrorCode: "insufficient_balance", ErrorMessage: "供货余额不足"}, nil
		}
		return nil, err
	}
	_ = s.repo.MarkSupplyOrderPaid(ctx, order.ID)
	// 锁卡（同一库存池；防超卖）
	res, err := s.inv.Reserve(ctx, 0, []invport.ReserveItem{{ProductID: productID, Quantity: req.GetQuantity()}})
	if err != nil || len(res.Cards) < int(req.GetQuantity()) {
		// 库存不足：账本退回 + rejected
		_ = s.repo.LedgerEntry(ctx, accountID, order.ID, "supply_refund", amount, "supply_order:"+req.GetDownstreamOrderNo()+":refund", "库存不足退回")
		_ = s.repo.MarkSupplyOrderRejected(ctx, order.ID)
		return &supplyv1.CreateSupplyOrderReply{Status: "rejected", ErrorCode: "no_stock", ErrorMessage: "库存不足"}, nil
	}
	cardIDs := make([]uint64, 0, len(res.Cards))
	for _, c := range res.Cards {
		cardIDs = append(cardIDs, c.CardID)
	}
	// 交付：MarkUsed + 解密卡密（内存态）
	if err := s.inv.MarkUsed(ctx, cardIDs, 0); err != nil {
		return nil, err
	}
	delivered, err := s.cards.Contents(ctx, cardIDs, productID, 0)
	if err != nil {
		s.log.Warn("supplier.delivery_read_failed", "order_id", order.ID, "err", err)
		return nil, errors.New("supplier.DELIVERY_FAILED")
	}
	_ = s.repo.MarkSupplyOrderFulfilled(ctx, order.ID)
	// 回调转发登记（T5）
	if req.GetCallbackUrl() != "" {
		_, _ = s.repo.CreateCallback(ctx, order.ID, accountID, req.GetDownstreamOrderNo(), req.GetCallbackUrl(), req.GetTraceId())
		s.EnqueueCallback(ctx, order.ID)
	}
	return &supplyv1.CreateSupplyOrderReply{
		SupplyOrderId: strconv.FormatUint(order.ID, 10),
		Status:        "fulfilled",
		Amount:        amount,
		Fulfillment:   &supplyv1.SupplyFulfillment{Status: "delivered", Cards: delivered},
	}, nil
}

// GetOrder 订单查询。
func (s *SupplyAPIService) GetOrder(ctx context.Context, req *supplyv1.GetSupplyOrderRequest) (*supplyv1.GetSupplyOrderReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_ORDER_ID")
	}
	o, err := s.repo.GetSupplyOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	return &supplyv1.GetSupplyOrderReply{
		SupplyOrderId:     strconv.FormatUint(o.ID, 10),
		DownstreamOrderNo: o.DownstreamOrderNo,
		Status:            string(o.Status),
		Amount:            o.Amount,
	}, nil
}

// CancelOrder 取消（未交付：账本退回 + rejected）。
func (s *SupplyAPIService) CancelOrder(ctx context.Context, req *supplyv1.CancelSupplyOrderRequest) (*supplyv1.CancelSupplyOrderReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_ORDER_ID")
	}
	o, err := s.repo.GetSupplyOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if string(o.Status) != "pending" && string(o.Status) != "paid" {
		return &supplyv1.CancelSupplyOrderReply{Ok: false}, nil // 已交付不可取消
	}
	_ = s.repo.LedgerEntry(ctx, o.AccountID, o.ID, "supply_refund", o.Amount, "supply_order:"+o.DownstreamOrderNo+":cancel", "取消退回")
	_ = s.repo.MarkSupplyOrderRejected(ctx, o.ID)
	return &supplyv1.CancelSupplyOrderReply{Ok: true}, nil
}

// RefundOrder 退款（已交付：账本退回；未交付：退回 + rejected）。
func (s *SupplyAPIService) RefundOrder(ctx context.Context, req *supplyv1.RefundSupplyOrderRequest) (*supplyv1.RefundSupplyOrderReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_ORDER_ID")
	}
	o, err := s.repo.GetSupplyOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	ref := "supply_order:" + o.DownstreamOrderNo + ":refund"
	err = s.repo.LedgerEntry(ctx, o.AccountID, o.ID, "supply_refund", o.Amount, ref, "退款")
	if err != nil && !errors.Is(err, ErrDuplicateLedger) {
		return &supplyv1.RefundSupplyOrderReply{Ok: false, ErrorCode: "refund_failed", ErrorMessage: err.Error()}, nil
	}
	if string(o.Status) != "fulfilled" {
		_ = s.repo.MarkSupplyOrderRejected(ctx, o.ID)
	}
	return &supplyv1.RefundSupplyOrderReply{Ok: true}, nil
}

// orderReply 幂等返回已存在订单。
func (s *SupplyAPIService) orderReply(o *ent.SupplyOrder) *supplyv1.CreateSupplyOrderReply {
	return &supplyv1.CreateSupplyOrderReply{
		SupplyOrderId: strconv.FormatUint(o.ID, 10),
		Status:        string(o.Status),
		Amount:        o.Amount,
	}
}
