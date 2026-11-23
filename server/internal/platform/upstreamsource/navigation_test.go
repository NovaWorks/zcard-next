package upstreamsource

import "testing"

func TestACGPageIDRoutes(t *testing.T) {
	for _, tt := range []struct{ page, link string }{{"1111", "https://supplier.example/shop/item/1111"}, {"", ""}, {"CA814E42E563538A", ""}, {"../1111", ""}, {"0", ""}, {"1.5", ""}} {
		got := Resolve(Input{ConnectionID: 1, Driver: "acg_faka", BaseURL: "https://supplier.example/shop", Code: "CA814E42E563538A", PageID: tt.page})
		if got["product_url"] != tt.link || got["product_code"] != "CA814E42E563538A" {
			t.Fatal(got)
		}
		if tt.link == "" && got["product_id"] != "" {
			t.Fatal("API code mislabeled as web ID", got)
		}
	}
	custom := Resolve(Input{ConnectionID: 1, Driver: "acg_faka", BaseURL: "https://supplier.example", Code: "ABC", Settings: map[string]any{"product_url_template": "{base}/find?code={code}"}})
	if custom["product_url"] != "https://supplier.example/find?code=ABC" {
		t.Fatal(custom)
	}
}
