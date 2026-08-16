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
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// DeliveryRepoImpl 交付仓储。
type DeliveryRepoImpl struct {
	data   *data.Data
	cipher *inventory.CardCipher
	inv    inventory.CardRepo
}

// NewDeliveryRepoImpl 构造。
func NewDeliveryRepoImpl(d *data.Data, cipher *inventory.CardCipher) *DeliveryRepoImpl {
	return &DeliveryRepoImpl{data: d, cipher: cipher}
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

	// 逐卡：MarkUsed + 交付记录
	var cardIDs []uint64
	for _, c := range cards {
		cardIDs = append(cardIDs, c.ID)

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
		_, err = client.OrderDelivery.Create().
			SetOrderID(o.ID).
			SetItemID(0).
			SetCardID(c.ID).
			SetDeliveryTokenHash(tokenHash).
			SetDeliveredMode(orderdelivery.DeliveredModeStatus).
			SetDeliveredBy(0). // auto
			SetFetchCount(0).
			SetDeliveredAt(deliveredAt).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("fulfillment.DELIVERY_CREATE_FAILED: %w", err)
		}
	}

	// 即删模式：交付后物理删除卡密行
	for _, it := range items {
		if it.FulfillmentType == orderitem.FulfillmentTypeAuto {
			// 检查商品 delivery_mode（简化：全部标记模式，即删 M1b 按商品配置）
		}
	}

	// 更新订单状态 → delivered
	_, err = client.Order.Update().
		Where(order.ID(o.ID), order.StatusEQ(order.StatusPaid)).
		SetStatus(order.StatusDelivered).
		SetVersion(o.Version + 1).
		Save(ctx)
	if err != nil {
		return err
	}

	// 状态事件溯源
	_, _ = client.OrderStatusEvent.Create().
		SetOrderID(o.ID).
		SetFromStatus(string(o.Status)).
		SetToStatus(string(order.StatusDelivered)).
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
	// 密码校验（constant-time；密码错与单号错对外表现一致）
	if o.QueryPasswordHash != "" {
		if !crypto.VerifyPassword(o.QueryPasswordHash, queryPassword) {
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

	isFirstFetch := true
	for _, d := range deliveries {
		if d.FetchCount > 0 {
			isFirstFetch = false
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
