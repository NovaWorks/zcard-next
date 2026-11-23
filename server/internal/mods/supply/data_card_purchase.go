package supply

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

func cardHash(v any) string { b, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }

type cardContext struct {
	conn    *ent.SupplyConnection
	product *ent.Product
	sku     *ent.ProductSku
	mapping *ent.SupplyMapping
}

func (g *Gateway) cardContext(ctx context.Context, req port.CardPurchaseRequest) (*cardContext, error) {
	c := data.Client(ctx, g.repo.data)
	p, e := c.Product.Get(ctx, req.ProductID)
	if e != nil {
		if ent.IsNotFound(e) {
			return nil, port.ErrCardConfigurationChanged
		}
		return nil, e
	}
	if p.UpstreamSourceID != req.ConnectionID || p.UpstreamProductCode != req.ProductCode || p.Status != 1 {
		return nil, port.ErrCardConfigurationChanged
	}
	conn, e := g.gate(ctx, req.ConnectionID)
	if e != nil {
		if ent.IsNotFound(e) {
			return nil, port.ErrCardConfigurationChanged
		}
		return nil, e
	}
	if string(conn.Status) != "active" {
		return nil, port.ErrCardConfigurationChanged
	}
	sk, e := data.DeliverySKU(ctx, c, p, req.SKUID)
	if e != nil {
		if ent.IsNotFound(e) {
			return nil, port.ErrCardConfigurationChanged
		}
		return nil, e
	}
	if sk != nil && sk.UpstreamSkuID != req.UpstreamSKU || sk == nil && req.UpstreamSKU != "" {
		return nil, port.ErrCardConfigurationChanged
	}
	if data.FulfillmentMode(p, sk) != "upstream" {
		return nil, port.ErrCardConfigurationChanged
	}
	m, e := c.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.LocalProductID(p.ID), supplymapping.UpstreamProduct(req.ProductCode), supplymapping.UpstreamSku("")).Only(ctx)
	if e != nil {
		if ent.IsNotFound(e) {
			return nil, port.ErrCardConfigurationChanged
		}
		return nil, e
	}
	return &cardContext{conn, p, sk, m}, nil
}
func cardConfiguration(v *cardContext) string {
	return cardHash([]any{data.SMSConnectionIdentity(v.conn), v.conn.Status, v.conn.Settings, v.conn.ExchangeRate, v.conn.PriceMarkupPercent, v.conn.PriceMarkupAmount, v.conn.PriceRoundingMode, v.conn.AutoSyncPrice, v.product.ID, v.product.LockVersion, v.product.Price, v.product.FactoryPrice, v.product.ControlConfig, v.sku, v.mapping.PricingOverride})
}
func (g *Gateway) CardConfiguration(ctx context.Context, req port.CardPurchaseRequest) (string, error) {
	v, e := g.cardContext(ctx, req)
	if e != nil {
		return "", e
	}
	return cardConfiguration(v), nil
}

type cardSelection struct {
	ID, Identity string
	Expires      int64
}

func selectionAAD(req port.CardPurchaseRequest) []byte {
	return []byte(fmt.Sprintf("card_selection:%d:%s:%s", req.ConnectionID, req.ProductCode, req.UpstreamSKU))
}
func (g *Gateway) openCardSelection(req port.CardPurchaseRequest, conn *ent.SupplyConnection) (string, error) {
	if req.Selection == "" {
		return "", nil
	}
	b, e := base64.RawURLEncoding.DecodeString(req.Selection)
	if e != nil || g.repo.box == nil {
		return "", fmt.Errorf("预选卡无效，请重新选择")
	}
	b, e = g.repo.box.Open(b, selectionAAD(req))
	if e != nil {
		return "", fmt.Errorf("预选卡不属于当前商品规格")
	}
	var v cardSelection
	if json.Unmarshal(b, &v) != nil || v.ID == "" || v.Expires <= time.Now().Unix() || v.Identity != data.SMSConnectionIdentity(conn) {
		return "", fmt.Errorf("预选卡已过期，请重新选择")
	}
	return v.ID, nil
}
func (g *Gateway) ListCardPicks(ctx context.Context, req port.CardPurchaseRequest, page, limit int) (*port.CardPickList, error) {
	v, e := g.cardContext(ctx, req)
	if e != nil {
		return nil, e
	}
	rules, e := cardpurchase.Decode(v.product.ControlConfig[cardpurchase.ConfigKey])
	if e != nil || rules == nil || !rules.Preselection {
		return nil, fmt.Errorf("%w: 商品不支持预选卡", port.ErrUpstreamUnavailable)
	}
	if page < 1 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: 预选卡分页无效", port.ErrUpstreamFormInvalid)
	}
	a, e := g.adapterFor(ctx, req.ConnectionID)
	if e != nil {
		return nil, e
	}
	picker, ok := a.(adapter.CardPicker)
	if !ok {
		return nil, port.ErrUpstreamUnavailable
	}
	list, e := picker.ListCardPicks(ctx, req.ProductCode, req.UpstreamSKU, page, limit)
	g.feedback(ctx, v.conn, e)
	if e != nil {
		return nil, cardReadError(e)
	}
	if g.repo.box == nil {
		return nil, fmt.Errorf("预选卡服务未就绪")
	}
	out := &port.CardPickList{Items: []port.CardPick{}, Total: list.Total}
	for _, p := range list.Items {
		b, _ := json.Marshal(cardSelection{p.ID, data.SMSConnectionIdentity(v.conn), time.Now().Add(5 * time.Minute).Unix()})
		sealed, e := g.repo.box.Seal(b, selectionAAD(req))
		if e != nil {
			return nil, e
		}
		premium := ApplyPricing(p.PremiumCents, v.conn.ExchangeRate, 0, 0, RoundingNone)
		out.Items = append(out.Items, port.CardPick{Selection: base64.RawURLEncoding.EncodeToString(sealed), Preview: p.Preview, PremiumCents: premium})
	}
	return out, nil
}

// Total pricing retains per-unit fixed markups but rounds only the complete
// upstream total, so non-divisible wholesale amounts are never multiplied twice.
func cardTotalPrice(v *cardContext, total, premium int64, quantity int) (int64, int64, error) {
	if total <= 0 || !money.ValidCents(total) || v.conn.ExchangeRate <= 0 {
		return 0, 0, fmt.Errorf("上游总价或汇率无效")
	}
	rate := decimal.NewFromFloat(v.conn.ExchangeRate)
	cost := decimal.NewFromInt(total).Mul(rate).Round(0).IntPart()
	unit := v.product.Price
	last, known := v.mapping.PricingOverride["last_synced_price"]
	if v.sku != nil {
		unit = v.sku.Price
		if unit <= 0 {
			unit = v.product.Price
		}
		prices, _ := v.mapping.PricingOverride["sku_prices"].(map[string]any)
		last, known = prices[v.sku.UpstreamSkuID]
	}
	rule, e := readProductRule(v.mapping.PricingOverride)
	if e != nil {
		return 0, 0, e
	}
	_, fixed := v.mapping.PricingOverride["price"]
	protected := !v.conn.AutoSyncPrice || rule == nil || fixed || !known || unit != toInt64(last)
	if protected {
		// A deliberately fixed local unit price remains fixed; selected cards
		// add their separately confirmed premium.
		amount := decimal.NewFromInt(unit).Mul(decimal.NewFromInt(int64(quantity))).Add(decimal.NewFromInt(premium).Mul(rate)).Round(0).IntPart()
		if amount <= 0 || !money.ValidCents(amount) || !money.ValidCents(cost) {
			return 0, 0, fmt.Errorf("商品总价超出允许范围")
		}
		return amount, cost, nil
	}
	percent := rule.Percent
	fixedAmount := rule.Amount
	switch rule.Mode {
	case PriceModeChannel:
		percent = v.conn.PriceMarkupPercent
		fixedAmount = v.conn.PriceMarkupAmount
	case PriceModeFixed:
		percent = 0
	case PriceModeEqual:
		percent = 0
		fixedAmount = 0
	case PriceModePending:
		return 0, 0, fmt.Errorf("商品尚未定价")
	}
	raw := decimal.NewFromInt(total).Mul(rate).Mul(decimal.NewFromInt(1).Add(decimal.NewFromFloat(percent).Div(decimal.NewFromInt(100)))).Add(decimal.NewFromInt(fixedAmount).Mul(decimal.NewFromInt(int64(quantity))))
	var amount int64
	switch string(v.conn.PriceRoundingMode) {
	case RoundingCeilInt:
		amount = raw.Div(decimal.NewFromInt(100)).Ceil().Mul(decimal.NewFromInt(100)).IntPart()
	case RoundingCeilTenth:
		amount = raw.Div(decimal.NewFromInt(10)).Ceil().Mul(decimal.NewFromInt(10)).IntPart()
	default:
		amount = raw.Round(0).IntPart()
	}
	if (rule.Mode == PriceModeChannel || rule.Mode == PriceModePercent) && (percent > 0 || fixedAmount > 0) {
		base := decimal.NewFromInt(total).Mul(rate)
		switch string(v.conn.PriceRoundingMode) {
		case RoundingCeilInt:
			base = base.Div(decimal.NewFromInt(100)).Ceil().Mul(decimal.NewFromInt(100))
		case RoundingCeilTenth:
			base = base.Div(decimal.NewFromInt(10)).Ceil().Mul(decimal.NewFromInt(10))
		default:
			base = base.Round(0)
		}
		if amount <= base.IntPart() {
			amount = base.IntPart() + 1
		}
	}
	if amount <= 0 || !money.ValidCents(amount) || !money.ValidCents(cost) {
		return 0, 0, fmt.Errorf("商品总价超出允许范围")
	}
	return amount, cost, nil
}

func (g *Gateway) QuoteCard(ctx context.Context, req port.CardPurchaseRequest) (*port.CardPurchaseQuote, error) {
	v, e := g.cardContext(ctx, req)
	if e != nil {
		return nil, e
	}
	rules, e := cardpurchase.Decode(v.product.ControlConfig[cardpurchase.ConfigKey])
	if e != nil {
		return nil, e
	}
	if rules == nil || !rules.DynamicQuote {
		return nil, port.ErrUpstreamUnavailable
	}
	if e = rules.Validate(req.Quantity); e != nil {
		return nil, fmt.Errorf("%w: %s", port.ErrUpstreamFormInvalid, e)
	}
	if req.Selection != "" && (!rules.Preselection || req.Quantity != 1) {
		return nil, fmt.Errorf("%w: 预选卡每次只能购买一份", port.ErrUpstreamFormInvalid)
	}
	id, e := g.openCardSelection(req, v.conn)
	if e != nil {
		return nil, fmt.Errorf("%w: %s", port.ErrUpstreamFormInvalid, e)
	}
	a, e := g.adapterFor(ctx, req.ConnectionID)
	if e != nil {
		return nil, e
	}
	q, ok := a.(adapter.CardQuoter)
	if !ok {
		return nil, port.ErrUpstreamUnavailable
	}
	hash := cardConfiguration(v)
	result, e := q.QuoteCard(ctx, adapter.CardQuoteRequest{ProductCode: req.ProductCode, UpstreamSKU: req.UpstreamSKU, Quantity: req.Quantity, CardID: id, ManualFormData: req.ManualFormData})
	g.feedback(ctx, v.conn, e)
	if e != nil {
		return nil, cardReadError(e)
	}
	if result == nil {
		return nil, fmt.Errorf("上游报价无效")
	}
	amount, cost, e := cardTotalPrice(v, result.TotalCents, result.SelectionPremiumCents, req.Quantity)
	if e != nil {
		return nil, e
	}
	current, e := g.CardConfiguration(ctx, req)
	if e != nil || current != hash {
		return nil, fmt.Errorf("商品或定价已变化，请重新获取报价")
	}
	expiry := time.Now().Add(5 * time.Minute).Unix()
	if result.ExpiresAt > 0 && result.ExpiresAt < expiry {
		expiry = result.ExpiresAt
	}
	if expiry <= time.Now().Unix() {
		return nil, fmt.Errorf("上游报价已过期")
	}
	premium := int64(0)
	if result.SelectionPremiumCents > 0 {
		base, _, err := cardTotalPrice(v, result.TotalCents-result.SelectionPremiumCents, 0, req.Quantity)
		if err != nil {
			return nil, err
		}
		premium = amount - base
		if premium < 0 {
			return nil, fmt.Errorf("预选卡报价无效")
		}
	}
	return &port.CardPurchaseQuote{ProviderAmountCents: result.TotalCents, ProviderCurrency: result.Currency, CostRate: decimal.NewFromFloat(v.conn.ExchangeRate).String(), PremiumCents: premium, AmountCents: amount, CostCents: cost, ProviderSelection: id, ProviderQuoteID: result.QuoteID, ConfigurationHash: hash, ExpiresAt: expiry}, nil
}

func (g *Gateway) verifyFrozenCardPurchase(ctx context.Context, req port.PurchaseRequest, a adapter.Adapter, conn *ent.SupplyConnection) error {
	if req.CardConfigurationHash == "" {
		return nil
	} // Historical orders retain their original route.
	r := port.CardPurchaseRequest{ConnectionID: req.ConnectionID, ProductID: req.CardProductID, SKUID: req.CardSKUID, ProductCode: req.ProductCode, UpstreamSKU: req.UpstreamSKU, Quantity: req.Quantity, ManualFormData: req.ManualFormData}
	h, e := g.CardConfiguration(ctx, r)
	if errors.Is(e, port.ErrCardConfigurationChanged) {
		return port.ErrCardQuoteChanged
	}
	if e != nil {
		return e
	}
	if h != req.CardConfigurationHash || req.MaxCostCents <= 0 {
		return port.ErrCardQuoteChanged
	}
	// ZCard's single-use quote already freezes its amount and is consumed by its
	// authenticated create endpoint. Direct ACG has no price-lock API.
	if conn.Driver == "acg_faka" {
		q, ok := a.(adapter.CardQuoter)
		if !ok {
			return port.ErrCardQuoteChanged
		}
		v, e := q.QuoteCard(ctx, adapter.CardQuoteRequest{ProductCode: req.ProductCode, UpstreamSKU: req.UpstreamSKU, Quantity: req.Quantity, CardID: req.CardID, ManualFormData: req.ManualFormData})
		if e != nil {
			if errors.Is(e, adapter.ErrPurchaseFormInvalid) || errors.Is(e, adapter.ErrProductUnavailable) || errors.Is(e, adapter.ErrProductDeleted) || errors.Is(e, adapter.ErrNoStock) {
				return port.ErrCardQuoteChanged
			}
			return e
		}
		if v == nil || decimal.NewFromInt(v.TotalCents).Mul(decimal.NewFromFloat(conn.ExchangeRate)).Round(0).IntPart() != req.MaxCostCents {
			return port.ErrCardQuoteChanged
		}
	}
	return nil
}

// Read-only card endpoints preserve known business rejections for callers to
// return 4xx. They must not look like transient 5xx and trigger long retries.
func cardReadError(e error) error {
	switch {
	case errors.Is(e, adapter.ErrNoStock):
		return port.ErrUpstreamNoStock
	case errors.Is(e, adapter.ErrPurchaseFormInvalid):
		return port.ErrUpstreamFormInvalid
	case errors.Is(e, adapter.ErrProductDeleted):
		return port.ErrUpstreamDeleted
	case errors.Is(e, adapter.ErrProductUnavailable), errors.Is(e, adapter.ErrNotSupported):
		return port.ErrUpstreamUnavailable
	default:
		return e
	}
}
