package fulfillment

// T1 自动交付管线 + T2 取货三重门 + T3 人工发货（P1-06 最后一个 M1a 模块）。
//
// 核心安全设计（§5.20.2 无明文快照）：
//   交付记录 = card_id 引用 + 一次性 delivery_token_hash——不存明文。
//   取货时现场解密返回，绝不落库明文（区别于 1.x card_content / 友商 Payload text）。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// DeliveryRepoImpl 交付仓储。
type DeliveryRepoImpl struct {
	data    *data.Data
	cipher  *inventory.CardCipher
	inv     inventory.CardRepo
	gate    auditport.RiskGate // P2-06 取货失败锁定（nil = 未装配跳过）
	auditor auditport.Auditor  // P2-06 取货安全审计（nil = 未装配跳过）
}

// actorTypeOf 订单归属（游客 user_id=0 → guest）。
func actorTypeOf(o *ent.Order) string {
	if o.UserID == 0 {
		return "guest"
	}
	return "user"
}

// NewDeliveryRepoImpl 构造。
func NewDeliveryRepoImpl(d *data.Data, cipher *inventory.CardCipher, gate auditport.RiskGate, auditor auditport.Auditor) *DeliveryRepoImpl {
	return &DeliveryRepoImpl{data: d, cipher: cipher, gate: gate, auditor: auditor}
}

// ── T1 自动交付管线 ─────────────────────────────────────────

// FulfillOrder 自动交付（order.paid 事件触发）：
// 1) 取订单 reserved 卡密 → 2) MarkUsed/即删 → 3) 写交付记录（card_id + 令牌）
func (r *DeliveryRepoImpl) FulfillOrder(ctx context.Context, orderNo string) error {
	client := data.Client(ctx, r.data)

	// 查订单
	o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("fulfillment.ORDER_NOT_FOUND")
	}
	if err != nil {
		return err
	}

	// 幂等：已有交付记录直接返回
	existing, _ := client.OrderDelivery.Query().
		Where(orderdelivery.OrderID(o.ID)).Exist(ctx)
	if existing {
		return nil
	}

	// 取 reserved 卡密
	cards, err := client.Card.Query().
		Where(card.OrderID(o.ID), card.StatusEQ(card.StatusReserved)).
		All(ctx)
	if err != nil {
		return err
	}

	// 取订单子项（获取 product_id 用于解密 AAD）
	items, err := client.OrderItem.Query().
		Where(orderitem.OrderID(o.ID)).All(ctx)
	if err != nil {
		return err
	}
	productIDMap := map[uint64]bool{}
	for _, it := range items {
		productIDMap[it.ProductID] = true
	}

	deliveredAt := time.Now().UTC()

	// 商品发货模式映射（products.delivery_mode：status=标记 / delete=即删）
	modeByProduct := map[uint64]string{}
	for _, it := range items {
		if _, ok := modeByProduct[it.ProductID]; ok {
			continue
		}
		if prod, err := client.Product.Get(ctx, it.ProductID); err == nil {
			modeByProduct[it.ProductID] = string(prod.DeliveryMode)
		}
	}
	modeOf := func(productID uint64) orderdelivery.DeliveredMode {
		if modeByProduct[productID] == "delete" {
			return orderdelivery.DeliveredModeDelete
		}
		return orderdelivery.DeliveredModeStatus
	}

	// 逐卡：MarkUsed + 交付记录（即删模式交付后物理删除卡密行）
	var deleteCardIDs []uint64
	for _, c := range cards {
		// MarkUsed（affected rows 校验防并发重发）
		affected, err := client.Card.Update().
			Where(card.ID(c.ID), card.StatusEQ(card.StatusReserved), card.OrderID(o.ID)).
			SetStatus(card.StatusUsed).
			SetUsedAt(deliveredAt).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("fulfillment.MARK_USED_FAILED: %w", err)
		}
		if affected == 0 {
			continue // 已处理（并发幂等）
		}

		// 交付记录（card_id 引用 + 一次性令牌哈希——不存明文）
		token := randomToken()
		tokenHash := hashToken(token)
		mode := modeOf(c.ProductID)
		_, err = client.OrderDelivery.Create().
			SetOrderID(o.ID).
			SetItemID(0).
			SetCardID(c.ID).
			SetDeliveryTokenHash(tokenHash).
			SetDeliveredMode(mode).
			SetDeliveredBy(0). // auto
			SetFetchCount(0).
			SetDeliveredAt(deliveredAt).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("fulfillment.DELIVERY_CREATE_FAILED: %w", err)
		}
		if mode == orderdelivery.DeliveredModeDelete {
			deleteCardIDs = append(deleteCardIDs, c.ID) // 即删：交付后物理删除（取货占位兜底见 FetchDelivery）
		}
	}
	if len(deleteCardIDs) > 0 {
		if _, err := client.Card.Delete().Where(card.IDIn(deleteCardIDs...)).Exec(ctx); err != nil {
			return fmt.Errorf("fulfillment.DELETE_MODE_FAILED: %w", err)
		}
	}

	// 直发商品（url/code）：同一链接/兑换码反复发货——写直发交付记录
	// （CardID=0，取货时从商品 direct_content 现场解密）。每订单项一条。
	localDelivered := len(cards) > 0 // 本地卡密项已同步交付
	for _, it := range items {
		p, err := client.Product.Get(ctx, it.ProductID)
		if err != nil || p.StockType == product.StockTypeCard || p.UpstreamSourceID > 0 {
			continue // 卡密类走上方逐卡；上游项由 procurement 回填
		}
		token := randomToken()
		_, err = client.OrderDelivery.Create().
			SetOrderID(o.ID).
			SetItemID(it.ID).
			SetCardID(0).
			SetDeliveryTokenHash(hashToken(token)).
			SetDeliveredMode(orderdelivery.DeliveredModeDirect).
			SetDeliveredBy(0).
			SetFetchCount(0).
			SetDeliveredAt(deliveredAt).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("fulfillment.DIRECT_CREATE_FAILED: %w", err)
		}
		localDelivered = true
	}

	// 状态推进：含「未到卡上游项」的订单不得落 delivered——procurement 采购仍在途
	// 或失败，订单却显示已发货，客户/后台均无卡密可看（曾无条件 paid→delivered，
	// 线上实测即症状）。全项已交付 → delivered；本地已交 + 上游在途 →
	// partially_delivered；纯上游在途 → fulfilling（前台 PAID_STATES 均按成功展示，
	// 到卡后由 AttachUpstreamDelivery 推进终态）。
	upstreamPending := false
	for _, it := range items {
		if it.FulfillmentType != orderitem.FulfillmentTypeUpstream {
			continue
		}
		ok, err := client.OrderDelivery.Query().Where(orderdelivery.ItemID(it.ID)).Exist(ctx)
		if err != nil {
			return err
		}
		if !ok {
			upstreamPending = true
			break
		}
	}
	nextStatus := order.StatusDelivered
	if upstreamPending {
		nextStatus = order.StatusFulfilling
		if localDelivered {
			nextStatus = order.StatusPartiallyDelivered
		}
	}

	// 更新订单状态（paid → 终态/在途态）
	_, err = client.Order.Update().
		Where(order.ID(o.ID), order.StatusEQ(order.StatusPaid)).
		SetStatus(nextStatus).
		SetVersion(o.Version + 1).
		Save(ctx)
	if err != nil {
		return err
	}

	// 状态事件溯源
	_, _ = client.OrderStatusEvent.Create().
		SetOrderID(o.ID).
		SetFromStatus(string(o.Status)).
		SetToStatus(string(nextStatus)).
		SetEvent("delivered").
		SetOperator("system").
		Save(ctx)

	return nil
}

// ── T2 取货三重门 ─────────────────────────────────────────

// FetchResult 取货结果。
type FetchResult struct {
	OrderNo  string
	Status   string
	Items    []FetchItem
	FetchCnt int32
}

// FetchItem 取货项。
type FetchItem struct {
	CardID  uint64
	Content string // 明文（首次）或掩码
	Masked  bool
}

// FetchDelivery 取货（三重门：单号+密码+限流；首次返回明文，之后掩码）。
func (r *DeliveryRepoImpl) FetchDelivery(ctx context.Context, orderNo, queryPassword, clientIP string) (*FetchResult, error) {
	client := data.Client(ctx, r.data)

	// 门 1+2：单号 + 查询密码（错误响应一致——防枚举）
	o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("order.NOT_FOUND")
	}
	if err != nil {
		return nil, err
	}
	// P2-06 取货锁定检查（连续失败 N 次锁 IP+订单组合；锁定期内正确密码也拒绝）
	if r.gate != nil {
		lockKey := "fetch:" + auditport.NormalizeIP(clientIP) + ":" + orderNo
		if locked, _ := r.gate.IsLocked(ctx, lockKey); locked {
			return nil, fmt.Errorf("delivery.LOCKED: 取货失败次数过多，请稍后再试")
		}
	}

	// 密码校验（constant-time；密码错与单号错对外表现一致）
	if o.QueryPasswordHash != "" {
		if !crypto.VerifyPassword(o.QueryPasswordHash, queryPassword) {
			// P2-06 失败计数锁定（达到阈值即锁）
			if r.gate != nil {
				_ = r.gate.LockFetchFailure(ctx, "fetch:"+auditport.NormalizeIP(clientIP)+":"+orderNo)
			}
			return nil, fmt.Errorf("order.NOT_FOUND")
		}
	}

	// 取交付记录
	deliveries, err := client.OrderDelivery.Query().
		Where(orderdelivery.OrderID(o.ID)).
		Order(ent.Asc(orderdelivery.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	result := &FetchResult{
		OrderNo:  o.OrderNo,
		Status:   string(o.Status),
		FetchCnt: 0,
	}
	// 取货审计（P2-06 T3：谁/何时/IP/订单——不含明文卡密）
	if r.auditor != nil {
		r.auditor.Security(ctx, auditport.SecurityEntry{
			ActorType: actorTypeOf(o), ActorID: o.UserID,
			Action: "delivery.fetch", IP: clientIP,
			Metadata: map[string]any{"order_no": orderNo, "order_id": o.ID},
		})
	}

	isFirstFetch := true
	for _, d := range deliveries {
		if d.FetchCount > 0 {
			isFirstFetch = false
		}

		// 直发交付（url/code 商品）：内容在商品 direct_content（CardID=0 无卡）
		if d.DeliveredMode == orderdelivery.DeliveredModeDirect {
			var productID uint64
			if d.ItemID > 0 {
				if it, e := client.OrderItem.Get(ctx, d.ItemID); e == nil {
					productID = it.ProductID
				}
			}
			p, perr := client.Product.Get(ctx, productID)
			if perr != nil || len(p.DirectContent) == 0 {
				result.Items = append(result.Items, FetchItem{CardID: 0, Content: "****（直发内容未配置）", Masked: true})
				continue
			}
			plain, derr := r.cipher.Open(p.DirectContent, p.ID, p.SubsiteID)
			if derr != nil {
				result.Items = append(result.Items, FetchItem{CardID: 0, Content: "****（解密失败）", Masked: true})
				continue
			}
			if isFirstFetch {
				result.Items = append(result.Items, FetchItem{CardID: 0, Content: string(plain), Masked: false})
			} else {
				result.Items = append(result.Items, FetchItem{CardID: 0, Content: maskContent(string(plain)), Masked: true})
			}
			continue
		}

		// 取卡密
		c, err := client.Card.Get(ctx, d.CardID)
		if ent.IsNotFound(err) {
			// 即删模式：卡密已物理删除——返回占位
			result.Items = append(result.Items, FetchItem{
				CardID: d.CardID, Content: "****（已删除）", Masked: true,
			})
			continue
		}
		if err != nil {
			return nil, err
		}

		// 现场解密（不落库明文）
		plain, err := r.cipher.Open(c.Content, c.ProductID, c.SubsiteID)
		if err != nil {
			result.Items = append(result.Items, FetchItem{
				CardID: c.ID, Content: "****（解密失败）", Masked: true,
			})
			continue
		}

		if isFirstFetch {
			// 首次取货：返回明文
			result.Items = append(result.Items, FetchItem{
				CardID: c.ID, Content: string(plain), Masked: false,
			})
		} else {
			// 后续取货：掩码（尾4位）
			result.Items = append(result.Items, FetchItem{
				CardID: c.ID, Content: maskContent(string(plain)), Masked: true,
			})
		}
	}

	// 更新取货计数 + IP（审计）
	if isFirstFetch && len(deliveries) > 0 {
		for _, d := range deliveries {
			_, _ = client.OrderDelivery.UpdateOne(d).
				SetFetchCount(d.FetchCount + 1).
				SetFetchedIP(clientIP).
				Save(ctx)
		}
		result.FetchCnt = 1

		// 订单状态 → completed
		_, _ = client.Order.Update().
			Where(order.ID(o.ID), order.StatusEQ(order.StatusDelivered)).
			SetStatus(order.StatusCompleted).
			SetVersion(o.Version + 1).
			Save(ctx)
		_, _ = client.OrderStatusEvent.Create().
			SetOrderID(o.ID).
			SetFromStatus(string(o.Status)).
			SetToStatus(string(order.StatusCompleted)).
			SetEvent("completed").
			SetOperator("user").
			SetClientIP(clientIP).
			Save(ctx)
	} else if len(deliveries) > 0 {
		result.FetchCnt = deliveries[0].FetchCount + 1
	}

	return result, nil
}

// ── T3 人工发货 ───────────────────────────────────────────

// ManualDeliver 手动交付（卡密内容或物流单号）。
func (r *DeliveryRepoImpl) ManualDeliver(ctx context.Context, orderNo, content, logisticsNo, remark string, adminID uint64) error {
	client := data.Client(ctx, r.data)

	o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("fulfillment.ORDER_NOT_FOUND")
	}
	if err != nil {
		return err
	}

	// 卡密内容模式
	if content != "" {
		lines := strings.Split(strings.TrimSpace(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// 加密后创建卡密 + 交付记录（同自动交付出口）
			// 简化：直接用第一个 product_id
			items, _ := client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
			if len(items) == 0 {
				return fmt.Errorf("fulfillment.NO_ITEMS")
			}
			productID := items[0].ProductID

			enc, err := r.cipher.Seal(line, productID, o.SubsiteID)
			if err != nil {
				return err
			}
			hash := r.cipher.ContentHash(line)

			// 创建卡密（直接 used 状态）
			c, err := client.Card.Create().
				SetProductID(productID).
				SetSubsiteID(o.SubsiteID).
				SetContent(enc).
				SetContentHash(hash).
				SetStatus(card.StatusUsed).
				SetOrderID(o.ID).
				SetUsedAt(time.Now().UTC()).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("fulfillment.CARD_CREATE_FAILED: %w", err)
			}

			// 交付记录
			token := randomToken()
			_, err = client.OrderDelivery.Create().
				SetOrderID(o.ID).
				SetItemID(0).
				SetCardID(c.ID).
				SetDeliveryTokenHash(hashToken(token)).
				SetDeliveredMode(orderdelivery.DeliveredModeStatus).
				SetDeliveredBy(adminID).
				SetFetchCount(0).
				SetDeliveredAt(time.Now().UTC()).
				Save(ctx)
			if err != nil {
				return err
			}
		}
	}

	// 物流模式（logistics JSON）
	if logisticsNo != "" {
		_, err = client.OrderDelivery.Create().
			SetOrderID(o.ID).
			SetItemID(0).
			SetCardID(0).
			SetDeliveryTokenHash(hashToken(randomToken())).
			SetDeliveredMode(orderdelivery.DeliveredModeStatus).
			SetDeliveredBy(adminID).
			SetLogistics(map[string]any{"tracking_no": logisticsNo, "remark": remark}).
			SetFetchCount(0).
			SetDeliveredAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			return err
		}
	}

	// 更新订单状态
	_, err = client.Order.Update().
		Where(order.ID(o.ID)).
		SetStatus(order.StatusDelivered).
		SetVersion(o.Version + 1).
		Save(ctx)
	if err != nil {
		return err
	}
	_, _ = client.OrderStatusEvent.Create().
		SetOrderID(o.ID).
		SetFromStatus(string(o.Status)).
		SetToStatus(string(order.StatusDelivered)).
		SetEvent("delivered").
		SetOperator("admin").
		SetOperatorID(adminID).
		SetReason(remark).
		Save(ctx)

	return nil
}

// ListPending 待人工发货列表。
func (r *DeliveryRepoImpl) ListPending(ctx context.Context, page, size int) ([]*ent.Order, error) {
	return data.Client(ctx, r.data).Order.Query().
		Where(order.StatusEQ(order.StatusPaid)).
		Order(ent.Desc(order.FieldID)).
		Offset((page - 1) * size).Limit(size).
		All(ctx)
}

// ListDeliveries 交付记录列表。
func (r *DeliveryRepoImpl) ListDeliveries(ctx context.Context, orderNo string, page, size int) ([]*ent.OrderDelivery, int64, error) {
	client := data.Client(ctx, r.data)
	q := client.OrderDelivery.Query().Order(ent.Desc(orderdelivery.FieldDeliveredAt))
	if orderNo != "" {
		o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil, 0, nil
		}
		if err != nil {
			return nil, 0, err
		}
		q = q.Where(orderdelivery.OrderID(o.ID))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Clone().Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
}

// ── 工具 ──

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("fulfillment: 随机数生成失败")
	}
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func maskContent(plain string) string {
	if len(plain) <= 4 {
		return "****"
	}
	return "****" + plain[len(plain)-4:]
}

// ── T5（P2-02）上游采购交付出口 ─────────────────────────────

// AttachUpstreamDelivery 上游卡密交付（P2-02 T4 交付出口）：
// 入参已是密文（procurement 侧 CardCipher.Seal 后透传），本层只负责：
//  1. 写 cards（status=used，order_id 绑定——前台取货按 order 关联，与本地卡密同链路）
//  2. 写 order_deliveries（card_id 引用 + 一次性令牌 + 掩码，无明文快照）
//
// 幂等：同 order_item 已交付直接返回（procurement 侧也以采购单状态机兜底）。
func (r *DeliveryRepoImpl) AttachUpstreamDelivery(ctx context.Context, orderID, itemID, productID uint64, items []port.UpstreamDeliveryItem) error {
	client := data.Client(ctx, r.data)

	// 幂等：该 order_item 已有上游交付记录（card_id 关联）→ 直接返回
	exists, err := client.OrderDelivery.Query().
		Where(orderdelivery.ItemID(itemID)).
		Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	deliveredAt := time.Now().UTC()
	for _, it := range items {
		// 上游卡密以「已用卡」形态入库（不进入可售库存池；防超卖语义不受影响）
		c, err := client.Card.Create().
			SetProductID(productID).
			SetSubsiteID(0).
			SetContent(it.SealedContent).
			SetContentHash(it.ContentHash).
			SetStatus(card.StatusUsed).
			SetOrderID(orderID).
			SetUsedAt(deliveredAt).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("fulfillment.UPSTREAM_CARD_CREATE_FAILED: %w", err)
		}
		token := randomToken()
		_, err = client.OrderDelivery.Create().
			SetOrderID(orderID).
			SetItemID(itemID).
			SetCardID(c.ID).
			SetDeliveryTokenHash(hashToken(token)).
			SetDeliveredMode(orderdelivery.DeliveredModeStatus).
			SetDeliveredBy(0). // auto（上游采购交付）
			SetFetchCount(0).
			SetDeliveredAt(deliveredAt).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("fulfillment.UPSTREAM_DELIVERY_CREATE_FAILED: %w", err)
		}
	}

	// 到卡推进终态：全部上游项已交付 → delivered（FulfillOrder 曾把含上游项的
	// 订单提前标 delivered，本方法此前不推进状态——上游到卡前后状态口径由
	// 此收口；paid/fulfilling/partially_delivered → delivered，幂等）
	ups, err := client.OrderItem.Query().Where(orderitem.OrderID(orderID)).All(ctx)
	if err != nil {
		return err
	}
	allAttached := true
	for _, it := range ups {
		if it.FulfillmentType != orderitem.FulfillmentTypeUpstream {
			continue
		}
		ok, err := client.OrderDelivery.Query().Where(orderdelivery.ItemID(it.ID)).Exist(ctx)
		if err != nil {
			return err
		}
		if !ok {
			allAttached = false
			break
		}
	}
	if allAttached {
		if _, err := client.Order.Update().
			Where(order.ID(orderID),
				order.StatusIn(order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered)).
			SetStatus(order.StatusDelivered).
			Save(ctx); err != nil {
			return fmt.Errorf("fulfillment.UPSTREAM_STATUS_FAILED: %w", err)
		}
	}
	return nil
}
