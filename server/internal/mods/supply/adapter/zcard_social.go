package adapter

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"time"
	"unicode/utf8"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

func (a *zCardAdapter) SocialSchema(ctx context.Context, productID string) (*supplyport.SocialSchema, error) {
	if productID == "" {
		return nil, fmt.Errorf("adapter.zcard: missing social product")
	}
	raw, err := a.request(ctx, "GET", "/api/supply/products/"+url.PathEscape(productID)+"/social/schema", nil, nil)
	if err != nil {
		return nil, err
	}
	var schema supplyport.SocialSchema
	if err = decodeZCard(raw, &schema); err != nil || schema.Version != 1 || schema.Revision == "" {
		return nil, fmt.Errorf("adapter.zcard: invalid social schema")
	}
	return &schema, nil
}

func (a *zCardAdapter) QuoteSocial(ctx context.Context, productID string, parameters supplyport.SocialParameters, revision string) (*supplyport.SocialQuote, error) {
	if productID == "" || revision == "" {
		return nil, fmt.Errorf("adapter.zcard: missing social quote identity")
	}
	body := struct {
		Parameters     supplyport.SocialParameters `json:"parameters"`
		SchemaRevision string                      `json:"schema_revision"`
	}{parameters, revision}
	raw, err := a.request(withoutRetries(ctx), "POST", "/api/supply/products/"+url.PathEscape(productID)+"/social/quotes", nil, body)
	if err != nil {
		return nil, err
	}
	var quote supplyport.SocialQuote
	if err = decodeZCard(raw, &quote); err != nil || quote.QuoteID == "" || quote.ProductID != productID || quote.SchemaRevision != revision || !maps.Equal(quote.Parameters, parameters) || quote.AmountCents <= 0 || quote.Currency != "CNY" || int64(quote.ExpiresAt) <= time.Now().Unix() {
		return nil, fmt.Errorf("adapter.zcard: invalid social quote")
	}
	return &quote, nil
}

func (a *zCardAdapter) CreateSocial(ctx context.Context, req supplyport.SocialPurchase) (*supplyport.SocialOrder, error) {
	if req.RequiredCapability != supplyport.SocialCapability || req.ProductID == "" || req.QuoteID == "" || req.SchemaRevision == "" || req.DownstreamOrderNo == "" || utf8.RuneCountInString(req.DownstreamOrderNo) > 64 || req.MaxSupplyAmountCents <= 0 || req.Currency != "CNY" {
		return nil, fmt.Errorf("adapter.zcard: invalid social purchase")
	}
	// Supply API integers are numeric JSON; the saved intent uses lossless Integer.
	body := struct {
		QuoteID              string                      `json:"quote_id"`
		ProductID            string                      `json:"product_id"`
		SchemaRevision       string                      `json:"schema_revision"`
		Parameters           supplyport.SocialParameters `json:"parameters"`
		DownstreamOrderNo    string                      `json:"downstream_order_no"`
		RequiredCapability   string                      `json:"required_capability"`
		MaxSupplyAmountCents int64                       `json:"max_supply_amount_cents"`
		Currency             string                      `json:"currency"`
	}{req.QuoteID, req.ProductID, req.SchemaRevision, req.Parameters, req.DownstreamOrderNo, req.RequiredCapability, int64(req.MaxSupplyAmountCents), req.Currency}
	raw, err := a.request(withoutRetries(ctx), "POST", "/api/supply/social/orders", nil, body)
	if err != nil {
		return nil, err
	}
	var order supplyport.SocialOrder
	if err = decodeZCard(raw, &order); err != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid social purchase receipt")
	}
	if order.SupplyOrderID == "" || order.DownstreamOrderNo != req.DownstreamOrderNo {
		return nil, fmt.Errorf("adapter.zcard: inconsistent social receipt")
	}
	if order.Charged {
		if order.Amount <= 0 || order.Amount > req.MaxSupplyAmountCents || order.Snapshot == nil || order.Snapshot.PaidAmountCents != order.Amount || order.Snapshot.RefundedAmountCents < 0 || order.Snapshot.RefundedAmountCents > order.Amount {
			return nil, fmt.Errorf("adapter.zcard: inconsistent charged social receipt")
		}
		if order.Status == "rejected" && (order.Snapshot.SettlementState != "refunded" || order.Snapshot.RefundedAmountCents != order.Amount || order.Snapshot.RefundReference == "") {
			return nil, fmt.Errorf("adapter.zcard: rejected social order has no confirmed refund receipt")
		}
	} else if order.Status != "rejected" || order.Amount != 0 || order.ErrorCode == "" {
		return nil, fmt.Errorf("adapter.zcard: inconsistent uncharged social rejection")
	}
	return &order, nil
}

func (a *zCardAdapter) QuerySocial(ctx context.Context, ids []string) ([]supplyport.SocialOrder, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return nil, fmt.Errorf("adapter.zcard: invalid social query batch")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, fmt.Errorf("adapter.zcard: invalid social query identity")
		}
		seen[id] = true
	}
	out := make([]supplyport.SocialOrder, 0, len(ids))
	for _, id := range ids {
		raw, err := a.request(ctx, "GET", "/api/supply/social/orders/"+url.PathEscape(id), nil, nil)
		if err != nil {
			return nil, err
		}
		var order supplyport.SocialOrder
		if err = decodeZCard(raw, &order); err != nil || order.SupplyOrderID != id {
			return nil, fmt.Errorf("adapter.zcard: invalid social order query")
		}
		out = append(out, order)
	}
	return out, nil
}

func (a *zCardAdapter) ActSocial(ctx context.Context, id, action, operationID string) (*supplyport.SocialOperation, error) {
	if id == "" || (action != "cancel" && action != "refill") || operationID == "" || utf8.RuneCountInString(operationID) > 64 {
		return nil, fmt.Errorf("adapter.zcard: invalid social operation")
	}
	raw, err := a.request(withoutRetries(ctx), "POST", "/api/supply/social/orders/"+url.PathEscape(id)+"/actions", nil, map[string]string{"action": action, "operation_id": operationID})
	if err != nil {
		return nil, err
	}
	return decodeSocialOperation(raw, operationID)
}

func (a *zCardAdapter) QuerySocialOperation(ctx context.Context, id, operationID string) (*supplyport.SocialOperation, error) {
	if id == "" || operationID == "" {
		return nil, fmt.Errorf("adapter.zcard: missing social operation identity")
	}
	raw, err := a.request(ctx, "GET", "/api/supply/social/orders/"+url.PathEscape(id)+"/actions/"+url.PathEscape(operationID), nil, nil)
	if err != nil {
		return nil, err
	}
	return decodeSocialOperation(raw, operationID)
}

func decodeSocialOperation(raw []byte, id string) (*supplyport.SocialOperation, error) {
	var operation supplyport.SocialOperation
	if err := decodeZCard(raw, &operation); err != nil || operation.OperationID != id {
		return nil, fmt.Errorf("adapter.zcard: invalid social operation receipt")
	}
	switch operation.Status {
	case "pending", "succeeded", "rejected", "unknown":
		return &operation, nil
	default:
		return nil, fmt.Errorf("adapter.zcard: invalid social operation status")
	}
}

var _ supplyport.SocialClient = (*zCardAdapter)(nil)
