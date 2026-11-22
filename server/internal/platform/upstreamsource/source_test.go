package upstreamsource

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveNavigation(t *testing.T) {
	for _, tt := range []struct {
		name, driver, base, code, sku, website, wantProduct, wantSKU string
		settings                                                     map[string]any
	}{
		{name: "zcard subpath and credentials", driver: "zcard", base: "https://user:secret@up.example/shop/api/supply?token=secret#secret", code: "a/b c", website: "https://up.example/shop", wantProduct: "https://up.example/shop/product/a%2Fb%20c"},
		{name: "flattened MCY", driver: "mcy_shop", base: "https://mcy.example", code: "12~52", website: "https://mcy.example", wantProduct: "https://mcy.example/item?id=12", wantSKU: "52"},
		{name: "native MCY", driver: "mcy_shop", base: "https://mcy.example", code: "12", sku: "63", website: "https://mcy.example", wantProduct: "https://mcy.example/item?id=12", wantSKU: "63"},
		{name: "unknown route", driver: "csn19", base: "https://up.example/api/v2", code: "12", website: "https://up.example"},
		{name: "custom query", driver: "other", base: "https://api.example", code: "a&b", sku: "x/y", website: "https://shop.example/store", wantProduct: "https://shop.example/store/item?id=a%26b&sku=x%2Fy", wantSKU: "x/y", settings: map[string]any{"website_url": "https://shop.example/store", "product_url_template": "{base}/item?id={code}&sku={sku_id}&api_key=secret"}},
		{name: "invalid scheme", driver: "other", base: "javascript:alert(1)", code: "12"},
		{name: "host placeholder forbidden", driver: "other", base: "https://up.example", code: "12", website: "https://up.example", settings: map[string]any{"product_url_template": "https://{code}.example/item"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(Input{ConnectionID: 9007199254740993, Driver: tt.driver, BaseURL: tt.base, Code: tt.code, SKU: tt.sku, Stage: "purchase", Settings: tt.settings})
			if got["website_url"] != tt.website || got["product_url"] != tt.wantProduct || got["sku_id"] != tt.wantSKU {
				t.Fatalf("%#v", got)
			}
			if got["connection_id"] != "9007199254740993" {
				t.Fatal("ID precision lost", got)
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), "secret") {
				t.Fatal("credential leaked", string(b))
			}
		})
	}
}
