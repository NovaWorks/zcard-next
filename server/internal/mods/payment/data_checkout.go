package payment

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/proto"
)

// Fee is a customer surcharge, not the provider's settlement cost.
// Amount on a payment remains the exact gross amount requested from the gateway.
type paymentPricing struct {
	Base   int64          `json:"base"`
	Fee    int64          `json:"fee"`
	Total  int64          `json:"total"`
	Rate   int64          `json:"rate"`
	Type   string         `json:"type"`
	Bearer string         `json:"bearer"`
	Method string         `json:"method"`
	Charge ChargeSnapshot `json:"charge"`
}

func validateFee(fee int64, kind, bearer string) error {
	if fee < 0 || (kind != "fixed" && kind != "percent") || (bearer != "merchant" && bearer != "user") || (kind == "percent" && fee > 10000) || (kind == "fixed" && fee > 100000000) {
		return errors.BadRequest("payment.INVALID_FEE", "手续费须为 0–100% 或 0–1000000 元，承担方须为商家或用户")
	}
	return nil
}
func validateRecommendation(label, description string) error {
	if utf8.RuneCountInString(label) > 6 || utf8.RuneCountInString(description) > 60 || strings.ContainsAny(label+description, "\r\n") {
		return errors.BadRequest("payment.INVALID_RECOMMENDATION", "推荐标签最多 6 字，说明最多 60 字，不能换行")
	}
	return nil
}
func (r *PaymentRepoImpl) price(ctx context.Context, ch *ent.PaymentChannel, base int64, method string) (paymentPricing, error) {
	p := paymentPricing{Base: base, Total: base, Rate: ch.Fee, Type: string(ch.FeeType), Bearer: string(ch.FeeBearer), Method: method}
	if err := validateFee(p.Rate, p.Type, p.Bearer); err != nil {
		return p, err
	}
	if base <= 0 || !money.ValidCents(base) {
		return p, errors.BadRequest("payment.INVALID_AMOUNT", "支付金额超出允许范围")
	}
	if ch.Driver == "wallet" {
		p.Rate = 0
		p.Bearer = "merchant"
	}
	if p.Bearer == "user" {
		p.Fee = p.Rate
		if p.Type == "percent" {
			p.Fee = (base*p.Rate + 5000) / 10000
		}
		p.Total += p.Fee
	}
	if !money.ValidCents(p.Total) {
		return p, errors.BadRequest("payment.INVALID_AMOUNT", "含手续费金额超出允许范围")
	}
	if ch.Driver != "wallet" && ch.Driver != "bepusdt" {
		var err error
		p.Charge, err = r.computeCharge(ctx, ch.Driver, r.DecryptConfig(ch), money.Cents(p.Total))
		if err != nil {
			return p, err
		}
	}
	return p, nil
}
func pricingOf(p *ent.Payment) paymentPricing {
	var out paymentPricing
	if len(p.PricingSnapshot) > 0 && json.Unmarshal(p.PricingSnapshot, &out) == nil {
		return out
	}
	return paymentPricing{Base: p.Amount, Total: p.Amount, Type: "fixed", Bearer: "merchant", Charge: ChargeSnapshot{Units: p.ChargedUnits, Currency: p.ChargedCurrency, Rate: p.ExchangeRate, Precision: p.ChargedPrecision}}
}
func pricingJSON(p paymentPricing) json.RawMessage { b, _ := json.Marshal(p); return b }
func pricingQuote(p paymentPricing, channel string, channelID uint64, scope string, paymentID uint64) *storefrontv1.PaymentQuote {
	// This is an optimistic version, not an authorization token. Every amount is recomputed server-side.
	raw, _ := json.Marshal(struct {
		P  paymentPricing
		C  uint64
		S  string
		ID uint64
	}{p, channelID, scope, paymentID})
	return &storefrontv1.PaymentQuote{BaseCents: p.Base, FeeCents: p.Fee, TotalCents: p.Total, FeeType: p.Type, FeeRate: p.Rate, FeeBearer: p.Bearer, QuoteKey: fmt.Sprintf("%x", sha256.Sum256(raw)), PaymentId: paymentID, Channel: channel, Method: p.Method, ChargedCurrency: p.Charge.Currency, ChargedUnits: p.Charge.Units, ChargedPrecision: p.Charge.Precision}
}
func checkQuote(ctx context.Context, q *storefrontv1.PaymentQuote) error {
	key := port.QuoteKey(ctx)
	if (key != "" && key != q.QuoteKey) || (key == "" && q.FeeCents > 0) {
		return errors.BadRequest("payment.QUOTE_CHANGED", "支付金额已变化，请刷新费用明细后再次确认")
	}
	return nil
}
func (s *StorePaymentService) QuotePayment(ctx context.Context, req *storefrontv1.PaymentQuoteRequest) (*storefrontv1.PaymentQuote, error) {
	scene := req.GetScene()
	if scene == "" {
		scene = scenePurchase
	}
	base := req.GetAmountCents()
	var subsite, orderID uint64
	scope := scene
	if scene == scenePurchase {
		o, err := data.Client(ctx, s.data).Order.Query().Where(order.OrderNo(req.GetOrderNo())).Only(ctx)
		if err != nil {
			return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
		}
		claims := identity.ClaimsFromContext(ctx)
		if !(claims != nil && o.UserID != 0 && claims.Subject == o.UserID) && (o.QueryPasswordHash == "" || !crypto.VerifyPassword(o.QueryPasswordHash, req.GetQueryPassword())) {
			return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
		}
		if o.Status != order.StatusPendingPayment || o.ExpiryReview || !time.Now().Before(o.ExpiredAt) {
			return nil, errors.BadRequest("payment.ORDER_NOT_PENDING", "订单不在待支付状态")
		}
		base, subsite, orderID, scope = o.TotalAmount, o.SubsiteID, o.ID, o.OrderNo
	} else if scene == sceneMemberRecharge || scene == sceneSupplyRecharge {
		if identity.ClaimsFromContext(ctx) == nil {
			return nil, errors.Unauthorized("identity.UNAUTHORIZED", "请先登录")
		}
	} else {
		return nil, errors.BadRequest("payment.SCENE_INVALID", "不支持的支付用途")
	}
	ch, err := resolveChannel(ctx, s.data, subsite, req.GetChannel())
	if err != nil || !ch.DeletedAt.IsZero() {
		return nil, errors.BadRequest("payment.CHANNEL_NOT_FOUND", "支付渠道不可用")
	}
	if err = checkPaymentUsage(ch, req.GetMethod(), scene); err != nil {
		return nil, errors.BadRequest("payment.SCENE_DISABLED", err.Error())
	}
	if ch.Driver == "wallet" && identity.ClaimsFromContext(ctx) == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "余额支付需登录")
	}
	if ch.Driver == "bepusdt" && orderID > 0 {
		old, e := data.Client(ctx, s.data).Payment.Query().Where(payment.ChannelID(ch.ID), payment.OrderID(orderID)).Order(ent.Desc(payment.FieldID)).First(ctx)
		if e == nil {
			if old.Status != payment.StatusPending || !time.Now().Before(old.ExpiresAt) {
				return nil, errors.BadRequest("payment.ORDER_EXPIRED", "支付已关闭或过期，请重新下单")
			}
			return pricingQuote(pricingOf(old), ch.Code, ch.ID, scope, old.ID), nil
		}
		if e != nil && !ent.IsNotFound(e) {
			return nil, e
		}
	}
	p, err := s.repo.price(ctx, ch, base, req.GetMethod())
	if err != nil {
		return nil, err
	}
	return pricingQuote(p, ch.Code, ch.ID, scope, 0), nil
}
func (s *AdminPaymentService) saveCheckout(ctx context.Context, ch *ent.PaymentChannel, bearer string, recommended bool, label, description string) (*ent.PaymentChannel, error) {
	if bearer == "" {
		bearer = string(ch.FeeBearer)
	}
	if err := validateFee(ch.Fee, string(ch.FeeType), bearer); err != nil {
		return nil, err
	}
	if ch.Driver == "wallet" && (ch.Fee != 0 || bearer == "user") {
		return nil, errors.BadRequest("payment.INVALID_FEE", "余额支付固定免手续费")
	}
	if err := validateRecommendation(label, description); err != nil {
		return nil, err
	}
	return data.Client(ctx, s.data).PaymentChannel.UpdateOneID(ch.ID).SetFeeBearer(paymentchannel.FeeBearer(bearer)).SetRecommended(recommended).SetRecommendLabel(strings.TrimSpace(label)).SetRecommendDescription(strings.TrimSpace(description)).Save(ctx)
}
func (s *AdminPaymentService) UpdateChannel(ctx context.Context, req *adminv1.UpdateChannelRequest) (*adminv1.Channel, error) {
	if req.Fee != nil && req.GetFee() < 0 {
		return nil, errors.BadRequest("payment.INVALID_FEE", "手续费不能为负数")
	}
	if req.Sort != nil && req.GetSort() < 0 {
		return nil, errors.BadRequest("payment.INVALID_SORT", "排序不能为负数")
	}
	var result *adminv1.Channel
	err := data.Tx(ctx, s.data, func(tx context.Context) error {
		old, e := data.Client(tx, s.data).PaymentChannel.UpdateOneID(req.GetId()).AddSort(0).Save(tx)
		if e != nil {
			return e
		}
		// Copy before filling missing fields: caller-owned protobufs must not be mutated.
		copyReq := proto.Clone(req).(*adminv1.UpdateChannelRequest)
		if copyReq.Fee == nil {
			copyReq.Fee = &old.Fee
		}
		if copyReq.Sort == nil {
			copyReq.Sort = &old.Sort
		}
		if copyReq.Enabled == nil {
			copyReq.Enabled = &old.Enabled
		}
		if copyReq.FeeBearer == nil {
			v := string(old.FeeBearer)
			copyReq.FeeBearer = &v
		}
		if copyReq.Recommended == nil {
			copyReq.Recommended = &old.Recommended
		}
		if copyReq.RecommendLabel == nil {
			copyReq.RecommendLabel = &old.RecommendLabel
		}
		if copyReq.RecommendDescription == nil {
			copyReq.RecommendDescription = &old.RecommendDescription
		}
		result, e = s.updateChannel(tx, copyReq)
		return e
	})
	return result, err
}

func paymentCreationError(err error) error {
	if errors.Code(err) == 400 {
		return err
	}
	return errors.InternalServer("payment.CREATE_FAILED", "创建支付失败")
}
