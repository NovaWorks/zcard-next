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
	"encoding/json"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	couponport "github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	memberlevelport "github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/shopspring/decimal"
)

// OrderUsecase 扩展（持有依赖）。
type OrderUsecase struct {
	Data       *data.Data
	Inv        port.Inventory
	Gen        *id.Generator
	MemberRate memberlevelport.RateResolver
	Coupon     couponport.CouponResolver
	Catalog    catalogport.PricingResolver
	Outbox     events.Writer // order.* 事件发布（M2：procurement 订阅 order.paid）
	Gate       auditport.RiskGate // P2-06 下单风控闸门（nil = 未装配跳过）
}

// NewOrderUsecaseDep 构造（wire 注入依赖版）。
func NewOrderUsecaseDep(d *data.Data, inv port.Inventory, gen *id.Generator, memberRate memberlevelport.RateResolver, coupon couponport.CouponResolver, cat catalogport.PricingResolver, outbox events.Writer, gate auditport.RiskGate) *OrderUsecase {
	return &OrderUsecase{Data: d, Inv: inv, Gen: gen, MemberRate: memberRate, Coupon: coupon, Catalog: cat, Outbox: outbox, Gate: gate}
}

// CreateOrderInput 下单输入。
type CreateOrderInput struct {
	Items          []OrderItemInput
	UserID         uint64 // 0=游客
	GuestContact   string
	QueryPassword  string // 明文（bcrypt 哈希后存储）
	Contact        string
	ClientIP       string
	SubsiteID      uint64
	CouponCode     string            // 优惠券码（可选）
	ControlAnswers map[string]string // 自定义控件答案（key=控件 ID，落 order.extra）
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

	// P2-06 下单风控闸门（事务前快速失败；事务内 pending 计数复查见 Gate 实现说明）
	if uc.Gate != nil && in.ClientIP != "" {
		if err := uc.Gate.Check(ctx, auditport.GateInput{RiskIP: in.ClientIP, UserID: in.UserID}); err != nil {
			return nil, err
		}
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

		// 3) 会员折扣（万分比，按用户累计消费匹配；解析失败降级为 0 不阻断下单）
		var memberRate int32
		if uc.MemberRate != nil && in.UserID > 0 {
			if r, _, err := uc.MemberRate.EffectiveRate(txCtx, in.UserID); err == nil {
				memberRate = r
			}
		}

		// 4) 算价管线（每商品行独立跑管线；会员折扣逐行，优惠券整单后置）
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

			// SKU 价 > 商品价（订单取价经 catalog port 解析；失败降级商品价）
			basePrice := money.Cents(p.Price)
			if item.SkuID > 0 && uc.Catalog != nil {
				if sp, err := uc.Catalog.ResolvePrice(txCtx, item.ProductID, item.SkuID); err == nil && sp > 0 {
					basePrice = sp
				}
			}
			// 会员商品组折扣（万分比；不命中/解析失败为 0）
			var groupRate int32
			if uc.Catalog != nil {
				if gr, err := uc.Catalog.ResolveGroupRate(txCtx, item.ProductID); err == nil {
					groupRate = gr
				}
			}

			pr := PriceCalculator(PriceInput{
				BasePrice:  basePrice,
				Quantity:   item.Quantity,
				MemberRate: memberRate,
				GroupRate:  groupRate,
			})
			results = append(results, itemResult{
				input: item, res: pr, cost: int64(p.FactoryPrice),
				productName: p.Name,
			})
			totalCents += int64(pr.Total)
		}

		// 4.5) 优惠券（整单一次性；percent 按会员折后应付额折算，不找零）
		var couponValue int64
		var couponID uint64
		if uc.Coupon != nil && in.CouponCode != "" {
			v, cid, err := uc.Coupon.Resolve(txCtx, in.CouponCode, in.UserID, money.Cents(totalCents))
			if err != nil {
				return fmt.Errorf("order.COUPON_INVALID: %w", err)
			}
			couponValue = int64(v)
			couponID = cid
		}
		if couponValue > totalCents {
			couponValue = totalCents
		}
		totalCents -= couponValue

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
		extra := map[string]any{}
		if len(in.ControlAnswers) > 0 {
			extra["control_answers"] = in.ControlAnswers
		}
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
			SetRiskIP(auditport.NormalizeIP(in.ClientIP)).
			SetExpiredAt(exp).
			SetVersion(0).
			SetExtra(extra).
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

		// 7.5) 优惠券金额行 + 核销
		if couponValue > 0 {
			_, err := client.OrderAmountLine.Create().
				SetOrderID(o.ID).
				SetType(orderamountline.TypeCouponDiscount).
				SetAmount(-couponValue).
				SetSourceType("coupon").
				SetSourceID(couponID).
				SetSeq(int32(len(results)*4 + 1)).
				Save(txCtx)
			if err != nil {
				return fmt.Errorf("order.COUPON_LINE_FAILED: %w", err)
			}
			if uc.Coupon != nil {
				if err := uc.Coupon.MarkUsed(txCtx, couponID, o.ID); err != nil {
					return fmt.Errorf("order.COUPON_MARK_FAILED: %w", err)
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
	uc.publishPaid(ctx, client, o)
	return nil
}

// publishPaid 发布 order.paid（P2-02 procurement 订阅；payload 含 upstream 项，
// 消费方无需回查订单——跨模块查询受限）。
func (uc *OrderUsecase) publishPaid(ctx context.Context, client *ent.Client, o *ent.Order) {
	if uc.Outbox == nil {
		return
	}
	items, err := client.OrderItem.Query().
		Where(orderitem.OrderID(o.ID)).
		All(ctx)
	if err != nil {
		return // 事件发布失败不阻断支付主流程（outbox 幂等，可后续补发）
	}
	type paidItem struct {
		OrderItemID     uint64 `json:"order_item_id"`
		ProductID       uint64 `json:"product_id"`
		SkuID           uint64 `json:"sku_id"`
		Quantity        int32  `json:"quantity"`
		FulfillmentType string `json:"fulfillment_type"`
	}
	payload := map[string]any{
		"order_no":   o.OrderNo,
		"order_id":   o.ID,
		"subsite_id": o.SubsiteID,
		"items":      []paidItem{},
	}
	rows := []paidItem{}
	for _, it := range items {
		rows = append(rows, paidItem{
			OrderItemID:     it.ID,
			ProductID:       it.ProductID,
			SkuID:           it.SkuID,
			Quantity:        it.Quantity,
			FulfillmentType: string(it.FulfillmentType),
		})
	}
	payload["items"] = rows
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = uc.Outbox.Write(ctx, "order", events.OrderPaid, o.OrderNo, DedupeKey(o.OrderNo, "paid"), raw)
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
