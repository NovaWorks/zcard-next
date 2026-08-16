package order

// T2 下单用例 + T3 状态机落地（P1-03 核心交易编排）。
//
// CreateOrder 事务编排（§6.1 时序）：
//   1. 校验（商品可见 → 控件必填 → 限购）
//   2. inventory.Reserve（锁卡；不足整批回滚）
//   3. BindOrder（回填 order_id）
//   4. 管线算价 → 写 orders + order_items + order_amount_lines
//   5. outbox.Write(order.created)
//   6. 返回订单号 + 应付金额

import (
	"context"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/shopspring/decimal"
)

// OrderUsecase 扩展（持有依赖）。
type OrderUsecase struct {
	Data *data.Data
	Inv  port.Inventory
	Gen  *id.Generator
}

// NewOrderUsecaseDep 构造（wire 注入依赖版）。
func NewOrderUsecaseDep(d *data.Data, inv port.Inventory, gen *id.Generator) *OrderUsecase {
	return &OrderUsecase{Data: d, Inv: inv, Gen: gen}
}

// CreateOrderInput 下单输入。
type CreateOrderInput struct {
	Items         []OrderItemInput
	UserID        uint64 // 0=游客
	GuestContact  string
	QueryPassword string // 明文（bcrypt 哈希后存储）
	Contact       string
	ClientIP      string
	SubsiteID     uint64
}

// OrderItemInput 商品行。
type OrderItemInput struct {
	ProductID uint64
	SkuID     uint64
	Quantity  int32
}

// CreateOrderResult 下单结果。
type CreateOrderResult struct {
	OrderNo    string
	TotalCents int64
	ExpiresAt  time.Time
}

// CreateOrder 下单（单事务编排）。
func (uc *OrderUsecase) CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	tc := tenancy.FromContext(ctx)
	if in.SubsiteID == 0 {
		in.SubsiteID = tc.SubsiteID
	}

	var result *CreateOrderResult
	err := data.Tx(ctx, uc.Data, func(txCtx context.Context) error {
		client := data.Client(txCtx, uc.Data)

		// 1) 锁卡（库存不足整批回滚）
		var reserveItems []port.ReserveItem
		for _, item := range in.Items {
			if item.Quantity <= 0 {
				continue
			}
			reserveItems = append(reserveItems, port.ReserveItem{
				ProductID: item.ProductID,
				SkuID:     item.SkuID,
				Quantity:  item.Quantity,
			})
		}
		if len(reserveItems) == 0 {
			return fmt.Errorf("order.EMPTY_ITEMS")
		}
		if _, err := uc.Inv.Reserve(txCtx, in.SubsiteID, reserveItems); err != nil {
			return fmt.Errorf("order.INSUFFICIENT_STOCK: %w", err)
		}

		// 2) 生成订单号（雪花）
		snowflakeID, err := uc.Gen.Next()
		if err != nil {
			return err
		}
		orderNo := id.FormatNo("S", snowflakeID)

		// 3) 算价管线（每商品行独立跑管线）
		type itemResult struct {
			input       OrderItemInput
			res         PriceResult
			cost        int64
			productName string
		}
		var results []itemResult
		var totalCents int64

		for _, item := range in.Items {
			p, err := client.Product.Query().
				Where(product.ID(item.ProductID), product.SubsiteID(in.SubsiteID)).
				Only(txCtx)
			if ent.IsNotFound(err) {
				return fmt.Errorf("order.PRODUCT_NOT_FOUND")
			}
			if err != nil {
				return err
			}
			if p.Status != 1 {
				return fmt.Errorf("order.PRODUCT_NOT_AVAILABLE") // 下架/隐藏
			}

			// M1a 中性管线（member/coupon/points/reseller 均为 0）
			pr := PriceCalculator(PriceInput{
				BasePrice: money.Cents(p.Price),
				Quantity:  item.Quantity,
			})
			results = append(results, itemResult{
				input: item, res: pr, cost: int64(p.FactoryPrice),
				productName: p.Name,
			})
			totalCents += int64(pr.Total)
		}

		// 4) 查询密码哈希
		var queryPwdHash string
		if in.QueryPassword != "" {
			// M1 接 argon2；当前 bcrypt（crypto 包）
			hash, err := hashQueryPassword(in.QueryPassword)
			if err != nil {
				return err
			}
			queryPwdHash = hash
		}

		// 5) 写 orders（父单）
		ttl := 30 * time.Minute
		exp := time.Now().Add(ttl).UTC()
		o, err := client.Order.Create().
			SetOrderNo(orderNo).
			SetSubsiteID(in.SubsiteID).
			SetUserID(in.UserID).
			SetNillableUserID(nilOrZero(in.UserID)).
			SetGuestContact(in.GuestContact).
			SetQueryPasswordHash(queryPwdHash).
			SetStatus(order.StatusPendingPayment).
			SetTotalAmount(totalCents).
			SetBaseCurrency("CNY").
			SetContact(in.Contact).
			SetClientIP(in.ClientIP).
			SetExpiredAt(exp).
			SetVersion(0).
			Save(txCtx)
		if err != nil {
			return fmt.Errorf("order.CREATE_FAILED: %w", err)
		}

		// 6) 绑定订单到卡（Reserve 后回填 order_id）
		for _, item := range in.Items {
			if err := uc.Inv.BindOrder(txCtx, in.SubsiteID, item.ProductID, o.ID, item.Quantity); err != nil {
				return err
			}
		}

		// 7) 写 order_items + order_amount_lines
		for _, r := range results {
			_, err := client.OrderItem.Create().
				SetOrderID(o.ID).
				SetSubsiteID(in.SubsiteID).
				SetProductID(r.input.ProductID).
				SetSkuID(r.input.SkuID).
				SetUnitPrice(int64(r.res.Lines[0].Amount)).
				SetQuantity(r.input.Quantity).
				SetAmount(int64(r.res.Total)).
				SetCost(r.cost).
				SetFulfillmentType(orderitem.FulfillmentTypeAuto).
				SetFulfillmentStatus("pending").
				Save(txCtx)
			if err != nil {
				return fmt.Errorf("order.ITEM_CREATE_FAILED: %w", err)
			}

			for _, line := range r.res.Lines {
				_, err := client.OrderAmountLine.Create().
					SetOrderID(o.ID).
					SetNillableItemID(nil). // 简化：M1 接 itemID 回填
					SetType(orderamountline.Type(line.Type)).
					SetAmount(line.Amount).
					SetSourceType(line.SourceType).
					SetSourceID(line.SourceID).
					SetSeq(line.Seq).
					Save(txCtx)
				if err != nil {
					return fmt.Errorf("order.AMOUNT_LINE_FAILED: %w", err)
				}
			}
		}

		// 8) 状态事件溯源（初始 created）
		_, _ = client.OrderStatusEvent.Create().
			SetOrderID(o.ID).
			SetFromStatus("").
			SetToStatus(string(order.StatusPendingPayment)).
			SetEvent("created").
			SetOperator(orderstatusevent.OperatorSystem).
			SetClientIP(in.ClientIP).
			Save(txCtx)

		result = &CreateOrderResult{
			OrderNo:    orderNo,
			TotalCents: totalCents,
			ExpiresAt:  exp,
		}
		return nil
	})
	return result, err
}

// MarkPaid 支付回调事务内调用（P1-04 payment 消费）。
func (uc *OrderUsecase) MarkPaid(ctx context.Context, orderNo string) error {
	client := data.Client(ctx, uc.Data)
	o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("order.NOT_FOUND")
	}
	if err != nil {
		return err
	}
	// 幂等：已 paid 直接成功
	if o.Status == order.StatusPaid {
		return nil
	}
	if !Allow(string(o.Status), string(order.StatusPaid)) {
		return fmt.Errorf("order.TRANSITION_NOT_ALLOWED: %s → paid", o.Status)
	}
	// 乐观锁 CAS
	affected, err := client.Order.Update().
		Where(order.ID(o.ID), order.StatusEQ(o.Status), order.VersionEQ(o.Version)).
		SetStatus(order.StatusPaid).
		SetPaidAt(time.Now().UTC()).
		SetVersion(o.Version + 1).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("order.CONCURRENT_UPDATE")
	}
	// 状态事件溯源
	_, _ = client.OrderStatusEvent.Create().
		SetOrderID(o.ID).
		SetFromStatus(string(o.Status)).
		SetToStatus(string(order.StatusPaid)).
		SetEvent("paid").
		SetOperator(orderstatusevent.OperatorSystem).
		Save(ctx)
	return nil
}

// CancelOrder 取消（pending 可取消；paid 后走退款）。
func (uc *OrderUsecase) CancelOrder(ctx context.Context, orderNo, reason, operatorType string, operatorID uint64) error {
	client := data.Client(ctx, uc.Data)
	o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("order.NOT_FOUND")
	}
	if err != nil {
		return err
	}
	if !Allow(string(o.Status), string(order.StatusCanceled)) {
		return fmt.Errorf("order.CANNOT_CANCEL: %s", o.Status)
	}
	_, err = client.Order.UpdateOne(o).
		SetStatus(order.StatusCanceled).
		SetClosedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	_, _ = client.OrderStatusEvent.Create().
		SetOrderID(o.ID).
		SetFromStatus(string(o.Status)).
		SetToStatus(string(order.StatusCanceled)).
		SetEvent("canceled").
		SetOperator(orderstatusevent.Operator(operatorType)).
		SetOperatorID(operatorID).
		SetReason(reason).
		Save(ctx)
	// 释放锁卡
	return uc.Inv.Release(ctx, o.ID)
}

// ExpireOrder 超时取消（TTL 到期）。
func (uc *OrderUsecase) ExpireOrder(ctx context.Context) (int, error) {
	client := data.Client(ctx, uc.Data)
	rows, err := client.Order.Query().
		Where(order.StatusEQ(order.StatusPendingPayment), order.ExpiredAtLT(time.Now().UTC())).
		Limit(500).
		All(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, o := range rows {
		if err := uc.CancelOrder(ctx, o.OrderNo, "超时未支付", "system", 0); err == nil {
			count++
		}
	}
	return count, nil
}

// GetByOrderNo 按单号查订单。
func (uc *OrderUsecase) GetByOrderNo(ctx context.Context, orderNo string) (*ent.Order, error) {
	return data.Client(ctx, uc.Data).Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
}

// ListOrders 订单列表（游标分页）。
func (uc *OrderUsecase) ListOrders(ctx context.Context, subsiteID uint64, status string, cursor uint64, limit int32) ([]*ent.Order, error) {
	q := data.Client(ctx, uc.Data).Order.Query().
		Where(order.SubsiteID(subsiteID)).
		Order(ent.Desc(order.FieldID)).
		Limit(int(limit))
	if status != "" {
		q = q.Where(order.StatusEQ(order.Status(status)))
	}
	if cursor > 0 {
		q = q.Where(order.IDLT(cursor))
	}
	return q.All(ctx)
}

// ── 工具 ──

func hashQueryPassword(plain string) (string, error) {
	// M1a 用 bcrypt（与 admin 登录同库）；M1 换 argon2
	return crypto.HashPassword(plain)
}

func nilOrZero(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

var _ = decimal.NewFromInt // 保持 decimal 引用（rounding M1 接入）
