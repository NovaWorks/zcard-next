package adapter

import (
	"context"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"net/url"
	"strings"
	"time"
)

// QuoteCard consumes a total for the complete selection. Never derive a unit
// price from it: quantity discounts and selection premiums can have remainders.
func (a *zCardAdapter) QuoteCard(ctx context.Context, req CardQuoteRequest) (*CardQuote, error) {
	if req.ProductCode == "" || req.Quantity < 1 || (req.CardID != "" && req.Quantity != 1) {
		return nil, ErrPurchaseFormInvalid
	}
	account, err := a.Ping(ctx)
	if err != nil {
		return nil, err
	}
	if len(account.Currency) != 3 || account.Currency != strings.ToUpper(account.Currency) {
		return nil, fmt.Errorf("上游未提供有效账户币种")
	}
	body := map[string]any{"sku_id": req.UpstreamSKU, "quantity": req.Quantity, "manual_form_data": req.ManualFormData, "card_selection": req.CardID}
	raw, err := a.request(ctx, "POST", "/api/supply/products/"+url.PathEscape(req.ProductCode)+"/card/quotes", nil, body)
	if err != nil {
		return nil, err
	}
	var reply struct {
		QuoteID   string              `json:"quote_id"`
		Amount    *supplyport.Integer `json:"amount_cents"`
		Premium   supplyport.Integer  `json:"premium_cents"`
		ExpiresAt supplyport.Integer  `json:"expires_at"`
		Quantity  supplyport.Integer  `json:"quantity"`
		Currency  string              `json:"currency"`
	}
	if err = decodeZCard(raw, &reply); err != nil || strings.TrimSpace(reply.QuoteID) == "" || reply.Amount == nil || *reply.Amount <= 0 || !money.ValidCents(int64(*reply.Amount)) || reply.Premium < 0 || reply.Premium > *reply.Amount || reply.Quantity != supplyport.Integer(req.Quantity) || reply.ExpiresAt <= supplyport.Integer(time.Now().Unix()) || reply.Currency != account.Currency {
		return nil, fmt.Errorf("上游卡密报价无效、过期或币种与账户不一致")
	}
	return &CardQuote{TotalCents: int64(*reply.Amount), SelectionPremiumCents: int64(reply.Premium), QuoteID: reply.QuoteID, ExpiresAt: int64(reply.ExpiresAt), Currency: reply.Currency}, nil
}

func (a *zCardAdapter) ListCardPicks(ctx context.Context, code, sku string, page, limit int) (*CardPickList, error) {
	if code == "" || page < 1 || limit < 1 || limit > 100 {
		return nil, ErrPurchaseFormInvalid
	}
	raw, err := a.request(ctx, "POST", "/api/supply/products/"+url.PathEscape(code)+"/card/drafts", nil, map[string]any{"sku_id": sku, "page": page, "limit": limit})
	if err != nil {
		return nil, err
	}
	var reply struct {
		Items []struct {
			Selection string             `json:"selection"`
			Preview   string             `json:"preview"`
			Premium   supplyport.Integer `json:"premium_cents"`
		} `json:"items"`
		Total supplyport.Integer `json:"total"`
	}
	if err = decodeZCard(raw, &reply); err != nil || reply.Total < 0 || len(reply.Items) > limit || reply.Total < supplyport.Integer(len(reply.Items)) {
		return nil, fmt.Errorf("上游选卡列表响应无效")
	}
	out := &CardPickList{Total: int(reply.Total), Items: make([]CardPick, 0, len(reply.Items))}
	seen := map[string]bool{}
	for _, item := range reply.Items {
		if item.Selection == "" || seen[item.Selection] || item.Premium < 0 || !money.ValidCents(int64(item.Premium)) {
			return nil, fmt.Errorf("上游选卡项目响应无效")
		}
		seen[item.Selection] = true
		out.Items = append(out.Items, CardPick{ID: item.Selection, Preview: item.Preview, PremiumCents: int64(item.Premium)})
	}
	return out, nil
}

// A selected opaque token is validated by QuoteCard with the full form and
// quantity. The public protocol deliberately has no raw single-card lookup.
func (*zCardAdapter) GetCardPick(context.Context, string, string, string) (*CardPick, error) {
	return nil, ErrNotSupported
}

var _ CardQuoter = (*zCardAdapter)(nil)
var _ CardPicker = (*zCardAdapter)(nil)
