package adapter

import (
	"context"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"net/url"
	"unicode/utf8"
)

func (a *zCardAdapter) CreateSMS(ctx context.Context, req supplyport.SMSPurchase) (*supplyport.SMSOrder, error) {
	if req.ProductID == "" || req.Quantity != 1 || (req.RequiredCapability != supplyport.SMSCapability && req.RequiredCapability != supplyport.SMSProductPurchase) || req.Currency != "CNY" || req.MaxSupplyAmountCents <= 0 || req.DownstreamOrderNo == "" || utf8.RuneCountInString(req.DownstreamOrderNo) > 64 {
		return nil, fmt.Errorf("adapter.zcard: invalid SMS purchase intent")
	}
	path := "/api/supply/orders"
	if req.RequiredCapability == supplyport.SMSProductPurchase {
		if req.SMSQuoteID == "" {
			return nil, fmt.Errorf("adapter.zcard: missing channel quote")
		}
		path = "/api/supply/sms/channel-orders"
	}
	// Keep the saved intent representation compatible while sending the supply
	// API's amount as a JSON integer rather than Integer's decimal string.
	body := struct {
		SMSQuoteID           string `json:"sms_quote_id,omitempty"`
		ProductID            string `json:"product_id"`
		Quantity             int    `json:"quantity"`
		DownstreamOrderNo    string `json:"downstream_order_no"`
		RequiredCapability   string `json:"required_capability"`
		MaxSupplyAmountCents int64  `json:"max_supply_amount_cents"`
		Currency             string `json:"currency"`
	}{
		SMSQuoteID:           req.SMSQuoteID,
		ProductID:            req.ProductID,
		Quantity:             req.Quantity,
		DownstreamOrderNo:    req.DownstreamOrderNo,
		RequiredCapability:   req.RequiredCapability,
		MaxSupplyAmountCents: int64(req.MaxSupplyAmountCents),
		Currency:             req.Currency,
	}
	raw, err := a.request(withoutRetries(ctx), "POST", path, nil, body)
	if err != nil {
		return nil, err
	}
	var out supplyport.SMSOrder
	if err = decodeZCard(raw, &out); err != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid SMS receipt")
	}
	if out.SupplyOrderID == "" || (out.Status == "rejected" && (out.Charged || out.Amount != 0 || out.ErrorCode == "")) || (out.Status != "rejected" && (!out.Charged || out.Amount <= 0 || out.Amount > req.MaxSupplyAmountCents)) {
		return nil, fmt.Errorf("adapter.zcard: inconsistent SMS receipt")
	}
	return &out, nil
}
func (a *zCardAdapter) QuerySMS(ctx context.Context, ids []string) ([]supplyport.SMSOrder, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return nil, fmt.Errorf("adapter.zcard: invalid SMS batch")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if id == "" || wanted[id] {
			return nil, fmt.Errorf("adapter.zcard: invalid SMS batch")
		}
		wanted[id] = true
	}
	raw, err := a.request(withoutRetries(ctx), "POST", "/api/supply/sms/sessions/query", nil, map[string]any{"supply_order_ids": ids})
	if err != nil {
		return nil, err
	}
	var out struct {
		Orders []supplyport.SMSOrder `json:"orders"`
	}
	if err = decodeZCard(raw, &out); err != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid SMS batch response")
	}
	for _, o := range out.Orders {
		if !wanted[o.SupplyOrderID] {
			return nil, fmt.Errorf("adapter.zcard: unexpected SMS order")
		}
		delete(wanted, o.SupplyOrderID)
	}
	if len(wanted) > 0 {
		return nil, fmt.Errorf("adapter.zcard: incomplete SMS batch")
	}
	return out.Orders, nil
}
func (a *zCardAdapter) ActSMS(ctx context.Context, id, action, operationID string) (*supplyport.SMSOperation, error) {
	if id == "" || (action != "cancel" && action != "finish") || operationID == "" || utf8.RuneCountInString(operationID) > 64 {
		return nil, fmt.Errorf("adapter.zcard: invalid SMS operation")
	}
	raw, err := a.request(withoutRetries(ctx), "POST", "/api/supply/orders/"+url.PathEscape(id)+"/sms/"+action, nil, map[string]string{"operation_id": operationID})
	if err != nil {
		return nil, err
	}
	var out supplyport.SMSOperation
	if err = decodeZCard(raw, &out); err != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid SMS operation response")
	}
	if out.OperationID != operationID || (out.Status != "pending" && out.Status != "succeeded" && out.Status != "rejected") {
		return nil, fmt.Errorf("adapter.zcard: inconsistent SMS operation response")
	}
	return &out, nil
}

func (a *zCardAdapter) GetSMSProduct(ctx context.Context, id string) (int64, error) {
	raw, err := a.request(ctx, "GET", "/api/supply/products/"+url.PathEscape(id), smsCatalogQuery(), nil)
	if err != nil {
		return 0, err
	}
	var out struct {
		Product zCardProduct `json:"product"`
	}
	if err = decodeZCard(raw, &out); err != nil {
		return 0, fmt.Errorf("adapter.zcard: invalid SMS product")
	}
	p := out.Product
	if idString(p.ID) != id || p.DeliveryKind != supplyport.SMSDelivery || !p.IsActive || p.Price <= 0 {
		return 0, fmt.Errorf("adapter.zcard: SMS product unavailable")
	}
	return int64(p.Price), nil
}

// RefreshProduct refreshes the selected public product only; omitted detail fields
// must not erase the category tree or description from the immutable page.
func (a *zCardAdapter) RefreshProduct(ctx context.Context, source *Product) (*Product, error) {
	raw, err := a.request(ctx, "GET", "/api/supply/products/"+url.PathEscape(source.ID), smsCatalogQuery(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Product zCardProduct `json:"product"`
	}
	if err = decodeZCard(raw, &out); err != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid product detail")
	}
	p := out.Product
	if idString(p.ID) != source.ID || source.DeliveryKind == supplyport.SMSDelivery && p.DeliveryKind != supplyport.SMSDelivery {
		return nil, fmt.Errorf("adapter.zcard: product identity mismatch")
	}
	copy := *source
	if normalizedProductKind(p.ProductKind) != normalizedProductKind(source.ProductKind) {
		return nil, fmt.Errorf("adapter.zcard: product kind changed")
	}
	copy.ProductKind = normalizedProductKind(p.ProductKind)
	copy.Price = int64(p.Price)
	copy.FactoryPrice = int64(p.Price)
	copy.IsActive = p.IsActive
	copy.Stock = p.Stock
	if p.Name != "" {
		copy.Name = p.Name
	}
	if p.CategoryID != nil {
		copy.CategoryID = idString(p.CategoryID)
	}
	if p.DescriptionSet {
		copy.Description = p.Description
		copy.DescriptionSet = true
	}
	if p.Cover != "" {
		copy.Cover = p.Cover
	}
	if p.DeliveryKind != "" {
		copy.DeliveryKind = p.DeliveryKind
	}
	if p.SMSProduct != nil {
		copy.SMSProduct = publicSMSProduct(p.SMSProduct)
	}
	return &copy, nil
}
