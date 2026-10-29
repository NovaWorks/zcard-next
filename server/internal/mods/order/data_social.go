package order

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialquote"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
	"time"
)

type frozenSocial struct {
	ConnectionID uint64                    `json:"connection_id"`
	Identity     string                    `json:"identity"`
	Request      supplyport.SocialPurchase `json:"request"`
	Hash         string                    `json:"hash"`
}

func schemaObject(schema supplyport.SocialSchema) map[string]any {
	raw, _ := json.Marshal(schema)
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	return obj
}
func socialHash(value any) string {
	b, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (uc *OrderUsecase) socialQuote(ctx context.Context, p *ent.Product, in CreateOrderInput) (*ent.SocialQuote, error) {
	c := data.Client(ctx, uc.Data)
	q, e := c.SocialQuote.Query().Where(socialquote.ID(in.SocialQuoteID), socialquote.Role("retail"), socialquote.ProductID(p.ID), socialquote.UserID(in.UserID), socialquote.SubsiteID(in.SubsiteID)).Only(ctx)
	if e != nil {
		return nil, fmt.Errorf("order.FORM_INVALID: 服务报价不存在或不属于当前会员")
	}
	if p.Status != 1 || p.DeliveryKind != supplyport.SocialDelivery || q.ConsumedBy != "" || q.ExpiresAt <= time.Now().Unix() || q.ProductRevision != p.LockVersion || q.ConnectionID != p.UpstreamSourceID || q.UpstreamProductID != p.UpstreamProductCode || q.CostCents <= 0 || q.AmountCents < q.CostCents || q.Currency != "CNY" {
		return nil, fmt.Errorf("order.FORM_INVALID: 服务报价已变化，请重新获取")
	}
	conn, e := c.SupplyConnection.Get(ctx, q.ConnectionID)
	if e != nil {
		return nil, e
	}
	m, e := c.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.LocalProductID(p.ID), supplymapping.UpstreamProduct(p.UpstreamProductCode), supplymapping.UpstreamSku("")).Only(ctx)
	if e != nil {
		return nil, e
	}
	if string(conn.Status) != "active" || conn.Driver != "zcard" || conn.ExchangeRate != 1 || data.SocialConnectionIdentity(conn) != q.ConnectionIdentity || data.SocialRetailPricingRevision(conn, m) != q.PricingRevision {
		return nil, fmt.Errorf("order.FORM_INVALID: 服务货源或定价已变化，请重新获取报价")
	}
	return q, nil
}

// createSocialOrder creates a native retail order. Service units live only in the
// frozen parameters; the internal order item represents one purchase obligation.
func (uc *OrderUsecase) createSocialOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if in.SubsiteID == 0 {
		in.SubsiteID = tenancy.FromContext(ctx).SubsiteID
	}
	if in.UserID == 0 || in.SubsiteID != 0 || len(in.Items) != 1 || in.Items[0].Quantity != 1 || in.Items[0].SkuID != 0 || in.SMSQuoteID != "" || in.UsePoints || in.CouponCode != "" || in.QuoteOnly || in.IdempotencyKey == "" || len(in.ControlAnswers) > 0 || len(in.Items[0].ControlAnswers) > 0 {
		return nil, fmt.Errorf("order.FORM_INVALID: 服务须由登录会员使用独立报价购买")
	}
	if old, e := uc.ReplayOrder(ctx, in); e != nil || old != nil {
		return old, e
	}
	if uc.AccessCipher == nil || uc.Gen == nil {
		return nil, fmt.Errorf("order.CREATE_FAILED: 服务购买暂不可用")
	}
	if uc.Gate != nil && in.ClientIP != "" {
		if e := uc.Gate.Check(ctx, auditport.GateInput{RiskIP: in.ClientIP, UserID: in.UserID}); e != nil {
			return nil, e
		}
	}
	var out *CreateOrderResult
	e := data.Tx(ctx, uc.Data, func(ctx context.Context) error {
		c := data.Client(ctx, uc.Data)
		if old, e := uc.ReplayOrder(ctx, in); e != nil {
			return e
		} else if old != nil {
			out = old
			return nil
		}
		p, e := data.ProductForDelivery(ctx, c, 0, in.Items[0].ProductID)
		if e != nil {
			return e
		}
		if p.DeliveryKind != supplyport.SocialDelivery || p.ProductKind != supplyport.SocialProductKind || p.Status != 1 || p.UpstreamSourceID == 0 || p.GoodsType == "physical" {
			return fmt.Errorf("order.FORM_INVALID: 服务商品不可售")
		}
		hidden, e := data.HiddenCategoryIDs(ctx, c, 0)
		if e != nil {
			return e
		}
		if data.CategoryHidden(hidden, p.CategoryID) {
			return fmt.Errorf("order.FORM_INVALID: 服务商品不可售")
		}
		if e := c.Product.UpdateOneID(p.ID).AddLockVersion(0).Exec(ctx); e != nil {
			return e
		}
		p, e = c.Product.Get(ctx, p.ID)
		if e != nil {
			return e
		}
		currency, e := data.BaseCurrency(ctx, uc.Data)
		if e != nil {
			return e
		}
		if currency != "CNY" {
			return fmt.Errorf("order.FORM_INVALID: 服务购买仅支持人民币基础币种")
		}
		q, e := uc.socialQuote(ctx, p, in)
		if e != nil {
			return e
		}
		raw, e := uc.AccessCipher.Open(q.ParamsCipher, q.ProductID, q.SubsiteID)
		if e != nil {
			return e
		}
		var params supplyport.SocialParameters
		if json.Unmarshal([]byte(raw), &params) != nil {
			return fmt.Errorf("order.FORM_INVALID: 报价参数无法读取")
		}
		var schema supplyport.SocialSchema
		if json.Unmarshal([]byte(q.SchemaJSON), &schema) != nil {
			return fmt.Errorf("order.FORM_INVALID: 报价定义无法读取")
		}
		if _, _, e := supplyport.ValidateSocialParameters(schema, params); e != nil {
			return e
		}
		snow, e := uc.Gen.Next()
		if e != nil {
			return e
		}
		no := id.FormatNo("S", snow)
		expires := time.Unix(q.ExpiresAt, 0)
		ttl := time.Now().Add(time.Duration(uc.ttlMinutes(ctx)) * time.Minute)
		if ttl.Before(expires) {
			expires = ttl
		}
		o, e := c.Order.Create().SetOrderNo(no).SetUserID(in.UserID).SetSubsiteID(0).SetStatus("pending_payment").SetTotalAmount(q.AmountCents).SetCost(q.CostCents).SetBaseCurrency("CNY").SetContact(in.Contact).SetClientIP(in.ClientIP).SetRiskIP(auditport.NormalizeIP(in.ClientIP)).SetExpiredAt(expires).SetRequestHash(orderRequestHash(in)).SetIdempotencyKey(orderIdemHash(in)).SetProfitEligible(false).Save(ctx)
		if e != nil {
			return e
		}
		it, e := c.OrderItem.Create().SetOrderID(o.ID).SetSubsiteID(0).SetProductID(p.ID).SetProductName(p.Name).SetDeliveryKind(supplyport.SocialDelivery).SetSocialSchema(schemaObject(schema)).SetQuantity(1).SetUnitPrice(q.AmountCents).SetAmount(q.AmountCents).SetCost(q.CostCents).SetPaidAmount(q.AmountCents).SetGoodsType("virtual").SetInventoryTracked(false).SetFulfillmentType("upstream").SetFulfillmentStatus("pending").Save(ctx)
		if e != nil {
			return e
		}
		request := supplyport.SocialPurchase{QuoteID: q.SupplyQuoteID, ProductID: q.UpstreamProductID, SchemaRevision: q.SchemaRevision, Parameters: params, DownstreamOrderNo: "social_" + uuid.NewString(), RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: supplyport.Integer(q.CostCents), Currency: "CNY"}
		frozen := frozenSocial{ConnectionID: q.ConnectionID, Identity: q.ConnectionIdentity, Request: request, Hash: socialHash(request)}
		b, _ := json.Marshal(frozen)
		sealed, e := uc.AccessCipher.Seal(string(b), it.ID, o.SubsiteID)
		if e != nil {
			return e
		}
		if e = c.OrderItem.UpdateOneID(it.ID).SetSocialPurchaseSnapshot(base64.RawStdEncoding.EncodeToString(sealed)).Exec(ctx); e != nil {
			return e
		}
		n, e := c.SocialQuote.Update().Where(socialquote.ID(q.ID), socialquote.ConsumedBy(""), socialquote.ExpiresAtGT(time.Now().Unix())).SetConsumedBy(no).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("order.FORM_INVALID: 报价已使用")
		}
		if e = c.OrderAmountLine.Create().SetOrderID(o.ID).SetItemID(it.ID).SetType("base_price").SetAmount(q.AmountCents).SetSourceType("social_quote").SetSeq(1).Exec(ctx); e != nil {
			return e
		}
		if e = c.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus("").SetToStatus("pending_payment").SetEvent("created").SetOperator("system").Exec(ctx); e != nil {
			return e
		}
		out = &CreateOrderResult{OrderNo: no, TotalCents: q.AmountCents, ExpiresAt: expires}
		return nil
	})
	if e != nil && ent.IsConstraintError(e) {
		if replay, replayErr := uc.ReplayOrder(ctx, in); replayErr != nil || replay != nil {
			return replay, replayErr
		}
	}
	return out, e
}
func (uc *OrderUsecase) readFrozenSocial(it *ent.OrderItem) (frozenSocial, error) {
	var f frozenSocial
	if uc.AccessCipher == nil {
		return f, fmt.Errorf("social cipher unavailable")
	}
	sealed, e := base64.RawStdEncoding.DecodeString(it.SocialPurchaseSnapshot)
	if e != nil {
		return f, e
	}
	raw, e := uc.AccessCipher.Open(sealed, it.ID, it.SubsiteID)
	if e != nil {
		return f, e
	}
	if json.Unmarshal([]byte(raw), &f) != nil || f.Hash != socialHash(f.Request) || f.Request.DownstreamOrderNo == "" {
		return f, fmt.Errorf("invalid social purchase snapshot")
	}
	return f, nil
}
func (uc *OrderUsecase) validateSocialPayment(ctx context.Context, oid uint64) error {
	c := data.Client(ctx, uc.Data)
	items, e := c.OrderItem.Query().Where(orderitem.OrderID(oid), orderitem.DeliveryKind(supplyport.SocialDelivery)).All(ctx)
	if e != nil {
		return e
	}
	for _, it := range items {
		o, e := c.Order.Get(ctx, oid)
		if e != nil {
			return e
		}
		q, e := c.SocialQuote.Query().Where(socialquote.Role("retail"), socialquote.ConsumedBy(o.OrderNo), socialquote.UserID(o.UserID), socialquote.ProductID(it.ProductID)).Only(ctx)
		if e != nil || q.ExpiresAt <= time.Now().Unix() {
			return fmt.Errorf("服务报价已过期，请重新下单")
		}
		f, e := uc.readFrozenSocial(it)
		if e != nil {
			return e
		}
		conn, e := c.SupplyConnection.Get(ctx, f.ConnectionID)
		if e != nil {
			return e
		}
		if data.SocialConnectionIdentity(conn) != f.Identity || string(conn.Status) != "active" {
			return fmt.Errorf("服务货源账号已变化，请重新下单")
		}
		p, e := c.Product.Get(ctx, it.ProductID)
		if e != nil {
			return e
		}
		m, e := c.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.LocalProductID(p.ID), supplymapping.UpstreamProduct(f.Request.ProductID), supplymapping.UpstreamSku("")).Only(ctx)
		if e != nil {
			return e
		}
		if p.LockVersion != q.ProductRevision || p.Status != 1 || p.UpstreamSourceID != conn.ID || p.UpstreamProductCode != f.Request.ProductID || data.SocialRetailPricingRevision(conn, m) != q.PricingRevision || q.AmountCents != o.TotalAmount || q.CostCents != int64(f.Request.MaxSupplyAmountCents) || q.SupplyQuoteID != f.Request.QuoteID {
			return fmt.Errorf("服务商品或定价已变化，请重新下单")
		}

	}
	return nil
}
func (uc *OrderUsecase) createSocialTasks(ctx context.Context, o *ent.Order) error {
	c := data.Client(ctx, uc.Data)
	items, e := c.OrderItem.Query().Where(orderitem.OrderID(o.ID), orderitem.DeliveryKind(supplyport.SocialDelivery)).All(ctx)
	if e != nil {
		return e
	}
	for _, it := range items {
		exists, e := c.SocialTask.Query().Where(socialtask.Role("retail"), socialtask.OrderItemID(it.ID)).Exist(ctx)
		if e != nil {
			return e
		}
		if exists {
			continue
		}
		if o.UserID == 0 || o.BaseCurrency != "CNY" || o.TotalAmount <= 0 || len(items) != 1 {
			return fmt.Errorf("invalid social retail order")
		}
		f, e := uc.readFrozenSocial(it)
		if e != nil {
			return e
		}
		conn, e := c.SupplyConnection.Get(ctx, f.ConnectionID)
		if e != nil {
			return e
		}
		if data.SocialConnectionIdentity(conn) != f.Identity {
			return fmt.Errorf("social account changed")
		}
		b, _ := json.Marshal(f.Request)
		sealed, e := uc.AccessCipher.Seal(string(b), it.ID, o.SubsiteID)
		if e != nil {
			return e
		}
		e = c.SocialTask.Create().SetID(uuid.NewString()).SetRole("retail").SetSubsiteID(o.SubsiteID).SetOrderID(o.ID).SetOrderItemID(it.ID).SetProductID(it.ProductID).SetUserID(o.UserID).SetConnectionID(f.ConnectionID).SetConnectionIdentity(f.Identity).SetQuoteID(f.Request.QuoteID).SetSchemaRevision(f.Request.SchemaRevision).SetRequestNo(f.Request.DownstreamOrderNo).SetRequestHash(f.Hash).SetRequestCipher(sealed).SetUpstreamProductID(f.Request.ProductID).SetRetailPaidAmount(o.TotalAmount).SetCostCents(int64(f.Request.MaxSupplyAmountCents)).Exec(ctx)
		if e != nil {
			return e
		}
	}
	return nil
}
