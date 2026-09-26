package supplier

// T3/T4 对外供货 API 实现（本站作上游，zcard-supply-v2 协议）。
//
// 下单链路（与前台共用同一库存池——防超卖）：
//   downstream_order_no 幂等 → 供货价核算（覆盖价 > 基础价）→ 账本扣款
//   （幂等键 supply_order_id:<id>:pay）→ inventory.Reserve 锁卡 → MarkUsed 交付
//   → 解密卡密（内存态）→ 响应 fulfillment.delivered + cards → 回调转发登记
//
// 余额不足/库存不足 → 明确错误码（下游可编程处理），不产生流水。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
	kerrors "github.com/go-kratos/kratos/v3/errors"

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
	pricing, err := s.repo.LoadPricing(ctx, accountID)
	if err != nil {
		return nil, err
	}
	reply := &supplyv1.ListProductsReply{
		Total:    total,
		PageSize: int32(pageSize),
		HasMore:  int64(page)*int64(pageSize) < total,
	}
	for _, p := range items {
		price := pricing.Price(p.ID, 0, p.CategoryID, p.Price)
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
	pricing, err := s.repo.LoadPricing(ctx, accountID)
	if err != nil {
		return nil, err
	}
	price := pricing.Price(row.ID, 0, row.CategoryID, row.Price)
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
	if _, err := s.reader.GetForSupply(ctx, id); err != nil {
		return nil, errors.New("supplier.PRODUCT_NOT_FOUND")
	}
	stock, err := s.inv.Stock(ctx, id, 0)
	if err != nil {
		return nil, err
	}
	return &supplyv1.GetStockReply{Stock: int32(stock)}, nil
}

// CreateOrder 下单：幂等 → 核算 → 扣款 → 锁卡 → 交付（zcard-supply-v2 协议壳）。
func (s *SupplyAPIService) CreateOrder(ctx context.Context, req *supplyv1.CreateSupplyOrderRequest) (*supplyv1.CreateSupplyOrderReply, error) {
	accountID := SupplyAccountID(ctx)
	if req.GetDownstreamOrderNo() == "" {
		return nil, errors.New("supplier.DOWNSTREAM_ORDER_NO_REQUIRED")
	}
	if req.GetQuantity() < 1 {
		return nil, errors.New("supplier.INVALID_QUANTITY")
	}
	productID, err := strconv.ParseUint(req.GetProductId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_PRODUCT_ID")
	}
	out, err := s.fulfillOrder(ctx, accountID, productID, req.GetQuantity(), req.GetDownstreamOrderNo(), req.GetCallbackUrl(), req.GetTraceId())
	if err != nil {
		return nil, err
	}
	if out.rejected {
		return &supplyv1.CreateSupplyOrderReply{Status: "rejected", ErrorCode: out.errCode, ErrorMessage: out.errMsg}, nil
	}
	reply := &supplyv1.CreateSupplyOrderReply{
		SupplyOrderId: strconv.FormatUint(out.order.ID, 10),
		Status:        string(out.order.Status),
		Amount:        out.amount,
	}
	if out.delivered {
		reply.Status = "fulfilled"
		reply.Fulfillment = &supplyv1.SupplyFulfillment{Status: "delivered", Cards: out.cards}
	}
	return reply, nil
}

// cardsPayloadOf 从 items 快照 card_ids 重建已交付订单卡密（兼容层查单/payload 用）。
func (s *SupplyAPIService) cardsPayloadOf(ctx context.Context, o *ent.SupplyOrder) ([]string, error) {
	if len(o.Items) == 0 {
		return nil, errors.New("items 快照缺失")
	}
	productID, _ := parseUintAny(o.Items[0]["product_id"])
	raw, _ := o.Items[0]["card_ids"].([]any)
	ids := make([]uint64, 0, len(raw))
	for _, v := range raw {
		if id, err := parseUintAny(v); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("无 card_ids")
	}
	return s.cards.Contents(ctx, ids, productID, 0)
}

// fulfillOutcome 协议无关下单结果（zcard v2 与兼容层 B/C 共用核心）。
type fulfillOutcome struct {
	order     *ent.SupplyOrder
	cards     []string // 交付卡密（内存态；delivered=false 时为空）
	amount    int64    // 分
	delivered bool
	rejected  bool   // 业务拒绝（下游可编程处理；err 为 nil 时有效）
	errCode   string // product_unavailable | insufficient_balance | no_stock
	errMsg    string
}

// fulfillOrder 下单核心（协议无关）：幂等 → 供货价 → 扣款 → 锁卡 → 交付 →
// 回调登记。items 快照带 card_ids（兼容层查单重建 payload 用）。
func (s *SupplyAPIService) fulfillOrder(ctx context.Context, accountID, productID uint64, quantity int32, downstreamOrderNo, callbackURL, traceID string) (*fulfillOutcome, error) {
	if accountID == 0 {
		return nil, kerrors.Unauthorized("supply.UNAUTHORIZED", "未认证下游账户")
	}
	if quantity < 1 || productID == 0 || downstreamOrderNo == "" || utf8.RuneCountInString(downstreamOrderNo) > 64 {
		return nil, kerrors.BadRequest("supply.INVALID_ORDER", "订单号、商品或数量非法")
	}
	var out *fulfillOutcome
	notify := false
	rejected := errors.New("supplier: rollback rejected order")
	err := data.Tx(ctx, s.repo.data, func(txctx context.Context) error {
		account, err := s.repo.lockAccount(txctx, accountID)
		if err != nil {
			return err
		}
		if account.Status != supplieraccount.StatusApproved {
			return kerrors.Forbidden("supply.ACCOUNT_DISABLED", "账户未获准供货")
		}
		out, notify, err = s.fulfillOrderTx(txctx, accountID, productID, quantity, downstreamOrderNo, callbackURL, traceID)
		if err == nil && out.rejected {
			return rejected
		}
		return err
	})
	if errors.Is(err, rejected) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	// 必须提交后再入队，不能把已关闭的事务 context 交给异步回调。
	if notify {
		s.EnqueueCallback(ctx, out.order.ID)
	}
	return out, nil
}

// fulfillOrderTx 的扣款、库存、卡密快照、状态与回调登记共用同一事务。
func (s *SupplyAPIService) fulfillOrderTx(ctx context.Context, accountID, productID uint64, quantity int32, downstreamOrderNo, callbackURL, traceID string) (*fulfillOutcome, bool, error) {
	existing, err := s.repo.GetSupplyOrderByNo(ctx, accountID, downstreamOrderNo)
	if err == nil {
		if len(existing.Items) == 0 {
			return nil, false, errors.New("supplier.INVALID_ORDER_ITEMS")
		}
		oldProduct, _ := parseUintAny(existing.Items[0]["product_id"])
		oldQuantity, _ := parseUintAny(existing.Items[0]["quantity"])
		if oldProduct != productID || oldQuantity != uint64(quantity) {
			return nil, false, kerrors.Conflict("supply.IDEMPOTENCY_CONFLICT", "同一订单号的商品和数量不能修改")
		}
		out := &fulfillOutcome{order: existing, amount: existing.Amount, delivered: string(existing.Status) == "fulfilled"}
		if out.delivered {
			out.cards, err = s.cardsPayloadOf(ctx, existing)
			if err != nil {
				return nil, false, err
			}
		}
		return out, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	p, err := s.reader.GetForSupply(ctx, productID)
	if err != nil || p.Status != 1 {
		return &fulfillOutcome{rejected: true, errCode: "product_unavailable", errMsg: "商品不可用"}, false, nil
	}
	pricing, err := s.repo.LoadPricing(ctx, accountID)
	if err != nil {
		return nil, false, err
	}
	price := pricing.Price(p.ID, 0, p.CategoryID, p.Price)
	if price <= 0 || price > math.MaxInt64/int64(quantity) {
		return nil, false, kerrors.BadRequest("supply.INVALID_AMOUNT", "供货金额非法")
	}
	amount := price * int64(quantity)
	items := []map[string]any{{"product_id": productID, "name": p.Name, "quantity": quantity, "unit_price": price}}
	order, err := s.repo.CreateSupplyOrder(ctx, accountID, downstreamOrderNo, items, amount)
	if err != nil {
		return nil, false, err
	}
	if err := s.repo.LedgerEntry(ctx, accountID, order.ID, "supply_pay", -amount, fmt.Sprintf("supply_order_id:%d:pay", order.ID), "下游下单扣款"); err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			return &fulfillOutcome{rejected: true, errCode: "insufficient_balance", errMsg: "供货余额不足"}, false, nil
		}
		return nil, false, err
	}
	if err := s.repo.MarkSupplyOrderPaid(ctx, order.ID); err != nil {
		return nil, false, err
	}
	res, err := s.inv.Reserve(ctx, 0, []invport.ReserveItem{{ProductID: productID, Quantity: quantity}})
	if err != nil || res == nil || len(res.Cards) != int(quantity) {
		return &fulfillOutcome{rejected: true, errCode: "no_stock", errMsg: "库存不足"}, false, nil
	}
	cardIDs := make([]uint64, 0, len(res.Cards))
	for _, c := range res.Cards {
		cardIDs = append(cardIDs, c.CardID)
	}
	if err := s.inv.MarkUsed(ctx, cardIDs, 0); err != nil {
		return nil, false, err
	}
	delivered, err := s.cards.Contents(ctx, cardIDs, productID, 0)
	if err != nil || len(delivered) != int(quantity) {
		return nil, false, errors.New("supplier.DELIVERY_FAILED")
	}
	items[0]["card_ids"] = cardIDs
	if err := s.repo.UpdateSupplyOrderItems(ctx, order.ID, items); err != nil {
		return nil, false, err
	}
	if err := s.repo.MarkSupplyOrderFulfilled(ctx, order.ID); err != nil {
		return nil, false, err
	}
	order, err = s.repo.GetAccountSupplyOrder(ctx, accountID, order.ID)
	if err != nil {
		return nil, false, err
	}
	if callbackURL != "" {
		if _, err := s.repo.CreateCallback(ctx, order.ID, accountID, downstreamOrderNo, callbackURL, traceID); err != nil {
			return nil, false, err
		}
	}
	return &fulfillOutcome{order: order, amount: amount, cards: delivered, delivered: true}, callbackURL != "", nil
}

// ListOrders 时间窗订单列表（对账数据源）。
func (s *SupplyAPIService) ListOrders(ctx context.Context, req *supplyv1.ListSupplyOrdersRequest) (*supplyv1.ListSupplyOrdersReply, error) {
	start := time.Unix(req.GetStart(), 0).UTC()
	end := time.Unix(req.GetEnd(), 0).UTC()
	if end.Before(start) || end.Sub(start) > 31*24*time.Hour {
		return nil, kerrors.BadRequest("supply.RANGE_INVALID", "时间窗非法（或超过 31 天）")
	}
	if SupplyAccountID(ctx) == 0 {
		return nil, kerrors.Unauthorized("supply.UNAUTHORIZED", "未认证下游账户")
	}
	rows, err := s.repo.ListAccountSupplyOrders(ctx, SupplyAccountID(ctx), start, end)
	if err != nil {
		return nil, kerrors.InternalServer("supply.LIST_FAILED", "读取订单失败")
	}
	reply := &supplyv1.ListSupplyOrdersReply{}
	for _, o := range rows {
		reply.Orders = append(reply.Orders, &supplyv1.SupplyOrderItem{
			Id: o.ID, DownstreamOrderNo: o.DownstreamOrderNo,
			Amount: o.Amount, Status: string(o.Status),
			CreatedAt: o.CreatedAt.Unix(),
		})
	}
	return reply, nil
}

// GetOrder 订单查询。
func (s *SupplyAPIService) GetOrder(ctx context.Context, req *supplyv1.GetSupplyOrderRequest) (*supplyv1.GetSupplyOrderReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_ORDER_ID")
	}
	o, err := s.accountOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	reply := &supplyv1.GetSupplyOrderReply{
		SupplyOrderId:     strconv.FormatUint(o.ID, 10),
		DownstreamOrderNo: o.DownstreamOrderNo,
		Status:            string(o.Status),
		Amount:            o.Amount,
	}
	// 已交付：回卡密（items 快照 card_ids → 解密重建；与 acg query 回 secret /
	// dujiao get order 回 payload 对齐——下游补查订单可取货）
	if string(o.Status) == "fulfilled" {
		if cards, err := s.cardsPayloadOf(ctx, o); err == nil && len(cards) > 0 {
			reply.Fulfillment = &supplyv1.SupplyFulfillment{Status: "delivered", Cards: cards}
		}
	}
	return reply, nil
}

// accountOrder 对外接口不允许缺省账户，也不区分外部订单与不存在的订单。
func (s *SupplyAPIService) accountOrder(ctx context.Context, orderID uint64) (*ent.SupplyOrder, error) {
	if SupplyAccountID(ctx) == 0 {
		return nil, kerrors.Unauthorized("supply.UNAUTHORIZED", "未认证下游账户")
	}
	o, err := s.repo.GetAccountSupplyOrder(ctx, SupplyAccountID(ctx), orderID)
	if errors.Is(err, ErrNotFound) {
		return nil, kerrors.NotFound("supply.ORDER_NOT_FOUND", "订单不存在")
	}
	return o, err
}

// CancelOrder 未交付订单取消；与退款共用事务和实际扣款上限。
func (s *SupplyAPIService) CancelOrder(ctx context.Context, req *supplyv1.CancelSupplyOrderRequest) (*supplyv1.CancelSupplyOrderReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_ORDER_ID")
	}
	if _, err := s.accountOrder(ctx, id); err != nil {
		return nil, err
	}
	ok, err := s.repo.RefundUndelivered(ctx, SupplyAccountID(ctx), id)
	if err != nil {
		return nil, err
	}
	return &supplyv1.CancelSupplyOrderReply{Ok: ok}, nil
}

// RefundOrder 已交付的卡密不可撤回，需管理员人工处理。
func (s *SupplyAPIService) RefundOrder(ctx context.Context, req *supplyv1.RefundSupplyOrderRequest) (*supplyv1.RefundSupplyOrderReply, error) {
	id, err := strconv.ParseUint(req.GetId(), 10, 64)
	if err != nil {
		return nil, errors.New("supplier.INVALID_ORDER_ID")
	}
	if _, err := s.accountOrder(ctx, id); err != nil {
		return nil, err
	}
	ok, err := s.repo.RefundUndelivered(ctx, SupplyAccountID(ctx), id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &supplyv1.RefundSupplyOrderReply{Ok: false, ErrorCode: "refund_not_allowed", ErrorMessage: "交付中或已交付订单需人工处理"}, nil
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
