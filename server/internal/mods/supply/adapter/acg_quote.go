package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// AccountQuoter resolves a single-unit quote for the authenticated account and
// every SKU. Catalog/reference prices must not substitute for a failed quote.
type AccountQuoter interface {
	QuoteProduct(context.Context, *Product) (*Product, error)
}

var quoteDecimal = regexp.MustCompile(`^[0-9]{1,13}(\.[0-9]{1,2})?$`)

func (a *acgFakaAdapter) accountQuote(ctx context.Context, code, sku string) (int64, error) {
	ctx, cancel := context.WithTimeout(stockReadContext(ctx), 10*time.Second)
	defer cancel()
	params := map[string]string{"code": code, "num": "1", "card_id": "0"}
	if sku != "" {
		fields, err := AcgSpecFormFields(sku)
		if err != nil {
			return 0, err
		}
		for k, v := range fields {
			params[k] = v
		}
	}
	body, err := a.signedPost(ctx, "/shared/commodity/valuation", params)
	if err != nil {
		return 0, err
	}
	raw, err := parseResp(body)
	if err != nil {
		return 0, err
	}
	var reply struct {
		Price json.RawMessage `json:"price"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return 0, err
	}
	return parseAccountPrice(reply.Price)
}

func parseAccountPrice(raw json.RawMessage) (int64, error) {
	value := string(raw)
	if len(value) > 0 && value[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
	}
	if !quoteDecimal.MatchString(value) {
		return 0, fmt.Errorf("上游未返回有效账号报价")
	}
	price, err := money.ParseDecimalStr(value, 2)
	if err != nil || price <= 0 {
		return 0, fmt.Errorf("上游账号报价必须大于零")
	}
	return int64(price), nil
}

func (a *acgFakaAdapter) QuoteProduct(ctx context.Context, p *Product) (*Product, error) {
	ctx, cancelQuote := context.WithTimeout(ctx, 60*time.Second)
	defer cancelQuote()
	// Prefer valuation. Only a proven missing route enables old inventory pricing.
	readCtx, cancel := context.WithTimeout(stockReadContext(ctx), 10*time.Second)
	inv, err := a.fetchInventory(readCtx, p.ID)
	cancel()
	if err != nil {
		return nil, err
	}
	if inv.DeliveryWay != 0 || inv.DraftStatus != 0 {
		return nil, fmt.Errorf("%w: 商品不支持自动采购报价", ErrNotSupported)
	}
	ini, err := parseAcgINI(inv.Config)
	if err != nil {
		return nil, err
	}
	combos, err := buildAcgCombos(ini, p.Price)
	if err != nil {
		return nil, err
	}
	available := make(map[string]bool, len(combos))
	for _, combo := range combos {
		available[combo.Code] = true
	}
	for _, sku := range p.SKUs {
		if !available[sku.ID] {
			return nil, fmt.Errorf("上游规格报价不完整，请刷新目录后重试")
		}
	}
	out := *p
	out.SKUs = nil
	out.Price = 0
	legacy := a.legacyQuote.Load()
	var legacyPrices map[string]int64
	modernQuoted := false
	quote := func(sku string) (int64, error) {
		// Bound per-combination traffic, including small catalogs on fast gateways.
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timer.C:
		}
		if !legacy {
			value, err := a.accountQuote(ctx, p.ID, sku)
			var he *httpError
			if err == nil {
				modernQuoted = true
				return value, nil
			}
			if modernQuoted || !errors.As(err, &he) || he.Code != "quote_route_missing" {
				return 0, err
			}
			legacy = true
			a.legacyQuote.Store(true)
		}
		if legacyPrices == nil {
			var err error
			legacyPrices, err = legacyAccountPrices(inv)
			if err != nil {
				return 0, err
			}
		}
		fields, err := AcgSpecFormFields(sku)
		if err != nil {
			return 0, err
		}
		value, ok := legacyPrices[fields["race"]]
		if !ok {
			return 0, fmt.Errorf("上游规格账号报价不完整")
		}
		return value, nil
	}
	if len(combos) == 0 {
		out.Price, err = quote("")
		if err != nil {
			return nil, err
		}
	} else {
		for _, combo := range combos {
			value, err := quote(combo.Code)
			if err != nil {
				return nil, err
			}
			out.SKUs = append(out.SKUs, SKU{ID: combo.Code, Code: combo.Code, Name: combo.Name, Price: value, Stock: -1, IsActive: true, SpecValues: specValuesOf(combo)})
			if out.Price == 0 || value < out.Price {
				out.Price = value
			}
		}
	}
	out.FactoryPrice = out.Price
	return &out, nil
}
