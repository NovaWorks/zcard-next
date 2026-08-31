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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	couponport "github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	memberlevelport "github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	paymentport "github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	resellerport "github.com/NovaWorks/zcard-next/server/internal/mods/reseller/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
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
	Outbox     events.Writer                  // order.* 事件发布（M2：procurement 订阅 order.paid）
	Gate       auditport.RiskGate             // P2-06 下单风控闸门（nil = 未装配跳过）
	Flash      couponport.FlashResolver       // M3：秒杀（nil 跳过）
	Promos     couponport.PromotionResolver   // M3：促销（nil 跳过）
	Settings   settingsport.SettingsReader    // M3：互斥开关读取（nil 默认互斥）
	Reseller   resellerport.Pricer            // P3-04：管线步骤 7 分站定价 + 防自购快照（nil 跳过）
	Points     walletport.PointsDebiter       // P3-01：积分兑换下单扣分（nil = 积分单不可用）
	SlowPay    paymentport.SlowPaymentChecker // P1-03：慢通道顺延探测（nil = 不顺延直接取消；newApp 破环点注入）
	StockGate  orderport.UpstreamStockGate         // P2-02 T4：上游代发项下单前实时库存预检（nil = 跳过；newApp 破环点注入）
}

// NewOrderUsecaseDep 构造（wire 注入依赖版）。
// SlowPay 经 SetSlowPaymentChecker 由 cmd/zcard newApp 注入（order↔payment wire 环破环点）。
func NewOrderUsecaseDep(d *data.Data, inv port.Inventory, gen *id.Generator, memberRate memberlevelport.RateResolver, coupon couponport.CouponResolver, cat catalogport.PricingResolver, outbox events.Writer, gate auditport.RiskGate, flash couponport.FlashResolver, promos couponport.PromotionResolver, settings settingsport.SettingsReader, reseller resellerport.Pricer, points walletport.PointsDebiter) *OrderUsecase {
	return &OrderUsecase{Data: d, Inv: inv, Gen: gen, MemberRate: memberRate, Coupon: coupon, Catalog: cat, Outbox: outbox, Gate: gate, Flash: flash, Promos: promos, Settings: settings, Reseller: reseller, Points: points}
}

// SetSlowPaymentChecker 慢通道探测注入（装配期一次；payment 侧实现，
// wire 环：OrderUsecase → payment/port.SlowPaymentChecker → PaymentRepoImpl →
// order/port.OrderLifecycle → OrderUsecase，故走 newApp 手工装配点）。
func (uc *OrderUsecase) SetSlowPaymentChecker(c paymentport.SlowPaymentChecker) {
	uc.SlowPay = c
}

// SetStockGate 上游库存预检闸门注入（装配期一次；supply 网关实现，
// order → supply 无 wire 依赖，同走 newApp 手工装配点）。
func (uc *OrderUsecase) SetStockGate(g orderport.UpstreamStockGate) {
	uc.StockGate = g
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
	UsePoints      bool              // P3-01：积分兑换下单（全部商品须为积分商品；同事务扣分直落 paid）
	IdempotencyKey string            // P1-03：下单幂等键（头 Idempotency-Key；同 key 返回首单）
	RefCode        string            // 推广归因码（游客/无链用户：实时解析推广者 → 订单级快照）
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

	// 交易设置校验（settings.trade；读取失败走保守默认——强制查询密码 + any 联系方式）
	if err := uc.validateTradeRequirements(ctx, in); err != nil {
		return nil, err
	}

	// P2-06 下单风控闸门（事务前快速失败；事务内 pending 计数复查见 Gate 实现说明）
	if uc.Gate != nil && in.ClientIP != "" {
		if err := uc.Gate.Check(ctx, auditport.GateInput{RiskIP: in.ClientIP, UserID: in.UserID}); err != nil {
			return nil, err
		}
	}

	// P2-02 T4：上游代发项实时库存预检（fail-open 闸门，事务前快速失败——
	// 上游明确无货直接拒单，不再让顾客"下单付款后等采购失败退款"。
	// 本地卡密项的强校验在下方事务内锁卡 Reserve；闸门自身查询失败/库存未知放行）
	if uc.StockGate != nil && len(in.Items) > 0 {
		gateItems := make([]orderport.UpstreamStockItem, 0, len(in.Items))
		for _, item := range in.Items {
			if item.Quantity > 0 {
				gateItems = append(gateItems, orderport.UpstreamStockItem{ProductID: item.ProductID, SkuID: item.SkuID, Quantity: item.Quantity})
			}
		}
		if len(gateItems) > 0 {
			if err := uc.StockGate.CheckItems(ctx, in.SubsiteID, gateItems); err != nil {
				return nil, fmt.Errorf("order.INSUFFICIENT_STOCK: %w", err)
			}
		}
	}

	var result *CreateOrderResult
	err := data.Tx(ctx, uc.Data, func(txCtx context.Context) error {
		client := data.Client(txCtx, uc.Data)

		// P1-03 Idempotency-Key：哈希落库唯一索引；同 key 双击返回首单（§7.3）
		var idemHash string
		if in.IdempotencyKey != "" {
			sum := sha256.Sum256([]byte(in.IdempotencyKey))
			idemHash = "idem-" + hex.EncodeToString(sum[:])
			if prev, err := client.Order.Query().
				Where(order.IdempotencyKey(idemHash)).Only(txCtx); err == nil {
				exp := prev.ExpiredAt
				if exp.IsZero() {
					exp = time.Now().Add(30 * time.Minute).UTC()
				}
				result = &CreateOrderResult{OrderNo: prev.OrderNo, TotalCents: prev.TotalAmount, ExpiresAt: exp}
				return nil // 幂等快路径：重复请求返回首次结果
			}
		}

		// P3-01：积分兑换前置（登录 + 引擎装配；游客/未装配直接拒绝不建单）
		if in.UsePoints {
			if in.UserID == 0 {
				return fmt.Errorf("order.POINTS_LOGIN_REQUIRED")
			}
			if uc.Points == nil {
				return fmt.Errorf("order.POINTS_UNAVAILABLE")
			}
		}

		// 1) 锁卡（库存不足整批回滚）。上游代发商品跳过本地锁卡——卡密在
		// 支付后由 procurement 向上游采购回填（P2-02 链路；本地池无其卡）。
		// url/code 直发商品同样跳过——直发内容商品级共享（同一链接/兑换码
		// 反复发货，无卡池概念），支付后由 fulfillment 直写交付记录。
		upstreamItem := map[uint64]bool{} // product_id → 是否上游项
		directItem := map[uint64]bool{}   // product_id → 是否直发项（url/code）
		var reserveItems []port.ReserveItem
		for _, item := range in.Items {
			if item.Quantity <= 0 {
				continue
			}
			p, err := client.Product.Query().
				Where(product.ID(item.ProductID), product.SubsiteID(in.SubsiteID)).
				Only(txCtx)
			if err != nil {
				continue // 商品校验在计价循环统一做（PRODUCT_NOT_FOUND）
			}
			if p.UpstreamSourceID > 0 {
				upstreamItem[item.ProductID] = true
				continue
			}
			if p.StockType != product.StockTypeCard {
				directItem[item.ProductID] = true
				continue // 链接/兑换码直发：不占卡池
			}
			reserveItems = append(reserveItems, port.ReserveItem{
				ProductID: item.ProductID,
				SkuID:     item.SkuID,
				Quantity:  item.Quantity,
			})
		}
		if len(reserveItems) > 0 {
			if _, err := uc.Inv.Reserve(txCtx, in.SubsiteID, reserveItems); err != nil {
				return fmt.Errorf("order.INSUFFICIENT_STOCK: %w", err)
			}
		}

		// 2) 生成订单号（雪花）
		snowflakeID, err := uc.Gen.Next()
		if err != nil {
			return err
		}
		orderNo := id.FormatNo("S", snowflakeID)

		// 3) 会员折扣（万分比，按用户累计消费匹配；解析失败降级为 0 不阻断下单）
		var memberRate int32
		var memberLevelID uint64
		if uc.MemberRate != nil && in.UserID > 0 {
			if r, lvl, err := uc.MemberRate.EffectiveRate(txCtx, in.UserID); err == nil {
				memberRate = r
				memberLevelID = lvl
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
		var pointsTotal int64               // P3-01：积分兑换单合计（积分单位）
		var cartItems []couponport.CartItem // 券范围判定输入
		flashApplied := false               // 券×秒杀互斥判据
		var totalSubsiteMarkup int64        // 分站加价合计（利润基数快照）

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
			// P3-01：积分兑换单——全部商品须为积分商品（混合单拒绝，口径清晰）
			if in.UsePoints {
				if p.PointsRequired <= 0 {
					return fmt.Errorf("order.POINTS_MIXED_CART")
				}
				pointsTotal += p.PointsRequired * int64(item.Quantity)
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

			// M3 步骤 4：秒杀（窗口判定 + 限购 + 同锁扣减——Reserve 已成功，同事务）
			var flashPrice money.Cents
			if uc.Flash != nil {
				if fs, err := uc.Flash.Active(txCtx, item.ProductID, item.SkuID); err == nil && fs != nil {
					// 限购：窗口内 paid+pending 累计
					if fs.PerUserLimit > 0 && in.UserID > 0 {
						if bought, err := uc.Flash.UserPurchasedCount(txCtx, item.ProductID, in.UserID, fs.StartAt); err == nil &&
							bought+item.Quantity > fs.PerUserLimit {
							return couponport.ErrFlashUserLimit
						}
					}
					// 同锁扣减（CAS 防超卖；失败回滚整个下单事务）
					if err := uc.Flash.Consume(txCtx, fs.ID, item.Quantity); err != nil {
						return err
					}
					flashPrice = fs.FlashPrice
					flashApplied = true
				}
			}

			// M3 步骤 4.5：促销（会员折扣后、券前；与秒杀互斥——flash 生效时跳过）
			var promoDiscount money.Cents
			var promoName string
			if uc.Promos != nil && flashPrice == 0 {
				if pi, err := uc.Promos.BestFor(txCtx, item.ProductID, p.CategoryID, basePrice); err == nil && pi != nil {
					if d := pi.DiscountFor(basePrice); d > 0 {
						promoDiscount, promoName = d, pi.Name
					}
				}
			}

			// M3 步骤 7：分站定价（listing 与 checkout 共用同一 ResolveUnitPrice——
			// 1.x 铁律，分站价只在一处计算）。秒杀价覆盖分站价（flash 生效时跳过）。
			var subsiteMarkup money.Cents
			if uc.Reseller != nil && in.SubsiteID != tenancy.MainSubsiteID && flashPrice == 0 {
				if sp, err := uc.Reseller.ResolveUnitPrice(txCtx, in.SubsiteID, item.ProductID, item.SkuID, basePrice); err == nil && sp > basePrice {
					subsiteMarkup = sp - basePrice
				}
			}
			totalSubsiteMarkup += int64(subsiteMarkup) * int64(item.Quantity)

			pr := PriceCalculator(PriceInput{
				BasePrice:     basePrice,
				Quantity:      item.Quantity,
				MemberRate:    memberRate,
				GroupRate:     groupRate,
				FlashPrice:    flashPrice,
				PromoDiscount: promoDiscount,
				PromoName:     promoName,
				SubsiteMarkup: subsiteMarkup,
			})
			results = append(results, itemResult{
				input: item, res: pr, cost: int64(p.FactoryPrice),
				productName: p.Name,
			})
			totalCents += int64(pr.Total)
			cartItems = append(cartItems, couponport.CartItem{
				ProductID: item.ProductID, CategoryID: p.CategoryID,
				Quantity: item.Quantity, UnitPrice: basePrice,
			})
		}

		// 4.6) 优惠券（整单一次性；范围矩阵 + 每人限用；券×秒杀互斥默认开）
		// P3-01：积分兑换单不参与券/金额管线——应付恒 0，凭据=积分流水（type=redeem）
		var couponValue int64
		var couponID uint64
		if in.UsePoints {
			totalCents = 0
		} else if uc.Coupon != nil && in.CouponCode != "" && !uc.flashCouponExclusive(txCtx, flashApplied) {
			v, cid, err := uc.Coupon.ResolveScoped(txCtx, in.CouponCode, in.UserID, memberLevelID, cartItems)
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
		if in.UsePoints {
			extra["points_total"] = pointsTotal // 积分口径快照（退款/审计读此处）
		}
		// P3-03：三级分销归因链快照（下单瞬间锁定）。优先级：
		// 1) 登录用户读自身邀请链（注册时绑定）；
		// 2) 链为空且带 ref_code（推广链接进站）→ 实时解析推广者订单级归因
		//    （不改用户链——一次性快照；游客/无链老用户下单均发佣）
		var invL1, invL2, invL3 uint64
		if in.UserID > 0 {
			if u, err := client.User.Get(txCtx, in.UserID); err == nil {
				invL1, invL2, invL3 = u.InviteL1, u.InviteL2, u.InviteL3
			}
		}
		if invL1 == 0 && in.RefCode != "" {
			if inviter := uc.resolveRefCode(txCtx, in.RefCode); inviter != nil && inviter.ID != in.UserID {
				invL1, invL2, invL3 = inviter.ID, inviter.InviteL1, inviter.InviteL2
			}
		}
		// P3-04：分站快照（下单瞬间锁定——分账与退款逆向只认快照，不回溯）
		profitEligible := true
		if uc.Reseller != nil && in.SubsiteID != tenancy.MainSubsiteID {
			profitEligible = uc.Reseller.ProfitEligible(txCtx, in.SubsiteID, in.UserID)
		}
		var subsiteDomain *string
		if in.SubsiteID != tenancy.MainSubsiteID && tc.Host != "" {
			host := tc.Host
			subsiteDomain = &host
		}
		create := client.Order.Create().
			SetOrderNo(orderNo).
			SetSubsiteID(in.SubsiteID).
			SetSubsiteProfit(totalSubsiteMarkup).
			SetProfitEligible(profitEligible).
			SetNillableSubsiteDomain(subsiteDomain).
			SetNillableInviteL1(nilOrZero(invL1)).
			SetNillableInviteL2(nilOrZero(invL2)).
			SetNillableInviteL3(nilOrZero(invL3)).
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
			SetExtra(extra)
		if idemHash != "" {
			create.SetIdempotencyKey(idemHash)
		}
		o, err := create.Save(txCtx)
		if ent.IsConstraintError(err) && idemHash != "" {
			// 并发同 key：唯一索引兜底——返回首单
			if prev, qerr := client.Order.Query().
				Where(order.IdempotencyKey(idemHash)).Only(txCtx); qerr == nil {
				result = &CreateOrderResult{OrderNo: prev.OrderNo, TotalCents: prev.TotalAmount, ExpiresAt: prev.ExpiredAt}
				return nil
			}
		}
		if err != nil {
			return fmt.Errorf("order.CREATE_FAILED: %w", err)
		}

		// 6) 绑定订单到卡（Reserve 后回填 order_id；上游/直发项无本地卡可绑）
		for _, item := range in.Items {
			if upstreamItem[item.ProductID] {
				continue
			}
			if directItem[item.ProductID] {
				continue
			}
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
				SetFulfillmentType(fulfillmentTypeOf(upstreamItem[r.input.ProductID])).
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

		// 9) P3-01 积分兑换收尾：同事务扣分（幂等键 points_pay:<orderNo>；
		//    不足整单回滚）→ 直落 paid（状态机 CAS + order.paid 事件 → 自动交付）。
		if in.UsePoints {
			if err := uc.Points.PointDebitInTx(txCtx, walletport.PointEntry{
				UserID:    in.UserID,
				Direction: "out",
				Type:      "redeem",
				Amount:    pointsTotal,
				Reference: "points_pay:" + orderNo,
				OrderID:   o.ID,
				Remark:    "积分商城兑换",
			}); err != nil {
				return fmt.Errorf("order.POINTS_INSUFFICIENT: %w", err)
			}
			if err := uc.MarkPaid(txCtx, orderNo); err != nil {
				return fmt.Errorf("order.POINTS_MARK_PAID_FAILED: %w", err)
			}
		}

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
	payload := map[string]any{
		"order_no":   o.OrderNo,
		"order_id":   o.ID,
		"subsite_id": o.SubsiteID,
		"user_id":    o.UserID,
		// P3-03：归因链快照（affiliate 消费；事件自带免回查）
		"invite_l1": o.InviteL1, "invite_l2": o.InviteL2, "invite_l3": o.InviteL3,
		"total_cents": o.TotalAmount,
		// 毛利口径基数（amount − cost；affiliate BaseScope=profit 消费）
		"profit_cents": o.TotalAmount - orderCostOf(items),
		// P3-04：分站快照（reseller 分账消费；自购快照不产生利润）
		"subsite_profit":  o.SubsiteProfit,
		"profit_eligible": o.ProfitEligible,
		"items":           rows,
	}
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
	if err := uc.Inv.Release(ctx, o.ID); err != nil {
		return err
	}
	// 券返还（取消恢复可用；过期不返由 coupon 侧口径保证）
	if uc.Coupon != nil {
		_ = uc.Coupon.ReturnByOrder(ctx, o.ID)
	}
	return nil
}

// ExpireOrder 超时取消（TTL 到期；T6）。
// 慢通道顺延（1.x 教训）：存在 usdt 族 pending 流水的订单不关闭——顺延一个 TTL
// 周期等待链上确认（探测失败保守顺延，fail-safe 不误杀）。
func (uc *OrderUsecase) ExpireOrder(ctx context.Context) (int, error) {
	client := data.Client(ctx, uc.Data)
	rows, err := client.Order.Query().
		Where(order.StatusEQ(order.StatusPendingPayment), order.ExpiredAtLT(time.Now().UTC())).
		Limit(500).
		All(ctx)
	if err != nil {
		return 0, err
	}
	ttl := 30 * time.Minute
	count := 0
	for _, o := range rows {
		if uc.SlowPay != nil {
			slow, err := uc.SlowPay.HasPendingSlowPayment(ctx, o.ID)
			if err != nil || slow {
				// 顺延一个 TTL（探测异常同样顺延——宁可慢杀不可误杀）
				_, _ = client.Order.UpdateOneID(o.ID).
					SetExpiredAt(time.Now().UTC().Add(ttl)).
					Save(ctx)
				continue
			}
		}
		if err := uc.CancelOrder(ctx, o.OrderNo, "超时未支付", "system", 0); err == nil {
			count++
		}
	}
	return count, nil
}

// ListUserOrders 用户订单列表（登录态「我的订单」；offset 分页——单用户量级安全）。
func (uc *OrderUsecase) ListUserOrders(ctx context.Context, userID uint64, status string, page, size int) ([]*ent.Order, int64, error) {
	if userID == 0 {
		return nil, 0, fmt.Errorf("order.USER_REQUIRED")
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 50 {
		size = 20
	}
	q := data.Client(ctx, uc.Data).Order.Query().
		Where(order.UserID(userID)).
		Order(ent.Desc(order.FieldID))
	if status != "" {
		q = q.Where(order.StatusEQ(order.Status(status)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Clone().Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
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

// flashCouponExclusive 券×秒杀互斥判定（settings.trade.flash_coupon_exclusive；
// 读取失败/未配置默认互斥——保守口径）。
func (uc *OrderUsecase) flashCouponExclusive(ctx context.Context, flashApplied bool) bool {
	if !flashApplied {
		return false
	}
	if uc.Settings == nil {
		return true
	}
	raw, err := uc.Settings.GetJSON(ctx, "trade", "flash_coupon_exclusive")
	if err != nil || len(raw) == 0 {
		return true
	}
	var v struct {
		Exclusive *bool `json:"flash_coupon_exclusive"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Exclusive == nil {
		return true
	}
	return *v.Exclusive
}

// resolveRefCode 推广码解析（双格式：8 位随机码 promo_code 匹配 / 旧数字 user_id）。
// 与 identity.ResolvePromoCode 同口径；模块不互相依赖（ent 直查，通道 A 免疫）。
func (uc *OrderUsecase) resolveRefCode(ctx context.Context, code string) *ent.User {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil
	}
	client := data.Client(ctx, uc.Data)
	if u, err := client.User.Query().Where(user.PromoCode(code)).Only(ctx); err == nil {
		return u
	}
	// 旧数字 user_id 兼容（存量推广链接）
	if isDigitStr(code) {
		var id uint64
		for _, c := range code {
			id = id*10 + uint64(c-'0')
		}
		if id > 0 {
			if u, err := client.User.Get(ctx, id); err == nil {
				return u
			}
		}
	}
	return nil
}

func isDigitStr(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// validateTradeRequirements 交易设置下单校验（settings.trade）：
//   - query_password（默认 true）：下单必须设置查询密码（≥4 位）——否则客户忘记
//     密码且无联系方式时订单无法找回
//   - contact_required（默认 any）：none|phone|email|qq|any——游客必须留对应
//     格式联系方式作为查询标识；登录用户已有账户可追溯，不强制
//
// 读取失败走保守默认（强制密码 + any）。
func (uc *OrderUsecase) validateTradeRequirements(ctx context.Context, in CreateOrderInput) error {
	// 查询密码强制（所有下单者——含登录用户：取货三重门的核心凭证）
	if uc.queryPasswordRequired(ctx) {
		if len(in.QueryPassword) < 4 {
			return fmt.Errorf("order.QUERY_PASSWORD_REQUIRED: 请设置查询密码（至少 4 位，取货时使用）")
		}
	}
	// 联系方式（仅游客；登录用户有账户可追溯；积分兑换游客在事务内 POINTS_LOGIN 拒绝，
	// 此处跳过避免拦截语义——联系方式校验只对常规游客单生效）
	if in.UserID == 0 && !in.UsePoints {
		mode := uc.contactRequired(ctx)
		contact := strings.TrimSpace(in.Contact)
		if contact == "" {
			contact = strings.TrimSpace(in.GuestContact)
		}
		if mode != "none" {
			if contact == "" {
				return fmt.Errorf("order.CONTACT_REQUIRED: 请填写联系方式（%s），用于订单查询与售后", contactModeLabel(mode))
			}
			if !contactMatchesMode(contact, mode) {
				return fmt.Errorf("order.CONTACT_INVALID: 联系方式格式不符（需要%s）", contactModeLabel(mode))
			}
		}
	}
	return nil
}

// queryPasswordRequired 查询密码强制开关（settings.trade.query_password；默认 true）。
func (uc *OrderUsecase) queryPasswordRequired(ctx context.Context) bool {
	if uc.Settings == nil {
		return true
	}
	raw, err := uc.Settings.GetJSON(ctx, "trade", "query_password")
	if err != nil || len(raw) == 0 {
		return true
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	return v
}

// contactRequired 联系方式要求（settings.trade.contact_required；默认 any）。
func (uc *OrderUsecase) contactRequired(ctx context.Context) string {
	if uc.Settings == nil {
		return "any"
	}
	raw, err := uc.Settings.GetJSON(ctx, "trade", "contact_required")
	if err != nil || len(raw) == 0 {
		return "any"
	}
	var v string
	if json.Unmarshal(raw, &v) != nil || v == "" {
		return "any"
	}
	return v
}

// contactModeLabel 联系方式要求显示名。
func contactModeLabel(mode string) string {
	switch mode {
	case "phone":
		return "手机号"
	case "email":
		return "邮箱"
	case "qq":
		return "QQ 号"
	default:
		return "邮箱或手机号等任一联系方式"
	}
}

// contactMatchesMode 联系方式格式校验（宽松口径：any=邮箱/手机/QQ 任一）。
func contactMatchesMode(contact, mode string) bool {
	isEmail := strings.Contains(contact, "@") && strings.Contains(contact, ".")
	isPhone := len(contact) >= 7 && len(contact) <= 15 && isDigitsOrPhone(contact)
	isQQ := isDigits(contact) && len(contact) >= 5 && len(contact) <= 12
	switch mode {
	case "phone":
		return isPhone
	case "email":
		return isEmail
	case "qq":
		return isQQ
	default: // any
		return isEmail || isPhone || isQQ
	}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isDigitsOrPhone(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '+' && r != '-' && r != ' ' {
			return false
		}
	}
	return true
}

// orderCostOf 订单成本合计（order_items 无成本列——按商品 factory_price 近似；
// M1 精确成本随采购联动， affiliate 毛利口径以事件快照为准）。
func orderCostOf(items []*ent.OrderItem) int64 {
	return 0 // 事件侧精确毛利 M4 采购成本回填后启用；当前毛利口径 = 金额（BaseScope=amount 默认）
}

// fulfillmentTypeOf 上游代发项 → upstream（procurement 订阅 order.paid 后向上游
// 采购）；本地商品 → auto（本地卡密履约）。
func fulfillmentTypeOf(isUpstream bool) orderitem.FulfillmentType {
	if isUpstream {
		return orderitem.FulfillmentTypeUpstream
	}
	return orderitem.FulfillmentTypeAuto
}
