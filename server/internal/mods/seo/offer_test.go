package seo

import (
	"encoding/json"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"strings"
	"testing"
)

func TestPublicFlashOfferInInitialHTML(t *testing.T) {
	for _, tc := range []struct {
		name      string
		now       int64
		remaining int32
		price     string
		stock     string
	}{
		{"active", 199, 3, "9.00", "InStock"}, {"expired", 200, 3, "49.00", "InStock"}, {"exhausted", 199, 0, "49.00", "OutOfStock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := storefrontProductSEO(&storefrontv1.Product{Id: 1, Name: "test", Stock: 20, PriceCents: 4900, FlashSale: &storefrontv1.FlashOffer{PriceCents: 900, Remaining: tc.remaining, EndAt: 200}}, tc.now)
			data := productPageData(testSite(), "", p)
			html, err := injectThemeHTML([]byte(`<html><head><title>old</title></head><body><div id="root"></div></body></html>`), data)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(html), `"price":"`+tc.price+`"`) || !strings.Contains(string(html), `https://schema.org/`+tc.stock) {
				t.Fatal(string(html))
			}
		})
	}
}

func TestPublicSKUOffersKeepIndividualPricesAndAvailability(t *testing.T) {
	p := storefrontProductSEO(&storefrontv1.Product{Id: 1, Stock: -2, PriceCents: 4900, Skus: []*storefrontv1.Sku{
		{Id: 100, PriceCents: 3900, FlashSale: &storefrontv1.FlashOffer{PriceCents: 900, Remaining: 3, EndAt: 200}},
		{Id: 200, PriceCents: 5900, FlashSale: &storefrontv1.FlashOffer{PriceCents: 900, Remaining: 0, EndAt: 200}},
	}}, 199)
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(productPageData(testSite(), "", p).JSONLD), &blocks); err != nil {
		t.Fatal(err)
	}
	offers := blocks[0]["offers"].([]any)
	first := offers[0].(map[string]any)
	second := offers[1].(map[string]any)
	if first["price"] != "9.00" || first["sku"] != "100" || first["availability"] != nil {
		t.Fatal(first)
	}
	if second["price"] != "59.00" || second["sku"] != "200" || second["availability"] != "https://schema.org/OutOfStock" {
		t.Fatal(second)
	}
}
