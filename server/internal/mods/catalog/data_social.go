package catalog

import (
	"bytes"
	"context"
	"encoding/json"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"google.golang.org/protobuf/types/known/structpb"
)

// Public catalogs preserve purchase rules without publishing the provider's cost.
// Purchases always use an authenticated total quote, never this display metadata.
func publicSocialSchema(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	b, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(b, &out) != nil {
		return nil
	}
	if billing, ok := out["billing"].(map[string]any); ok {
		delete(billing, "rate")
		billing["currency"] = "CNY"
		billing["mode"] = "quoted_total"
	}
	// Imported display metadata must be repriced for this shop by the gateway.
	delete(out, "display_price")
	return out
}

func (s *StoreCatalogService) addSocialDisplayPrices(ctx context.Context, products []port.Product, out []*storefrontv1.Product) {
	reader, ok := s.stockLookup.(port.SocialDisplayPriceReader)
	if !ok {
		return
	}
	prices := reader.DisplaySocialPrices(ctx, products)
	for _, p := range out {
		price, ok := prices[p.Id]
		if !ok || p.SocialSchema == nil || price.Currency != "CNY" || price.AmountCents <= 0 || price.Quantity <= 0 {
			continue
		}
		value := p.SocialSchema.AsMap()
		value["display_price"] = map[string]any{"amount_cents": price.AmountCents, "quantity": price.Quantity, "currency": price.Currency}
		p.SocialSchema, _ = structpb.NewStruct(value)
	}
}

func socialStruct(raw map[string]any) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	s, _ := structpb.NewStruct(publicSocialSchema(raw))
	return s
}

func sameSocialSchema(a, b map[string]any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}
