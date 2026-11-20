package supplier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/mcysign"
)

func mcyCall(t *testing.T, handler http.Handler, path, key, secret string, form url.Values, headerAuth bool) map[string]any {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if !headerAuth {
		form.Set("app_id", key)
		form.Set("app_key", secret)
	}
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	signature := adapter.AcgFakaSign(params, secret)
	if headerAuth {
		signature = mcysign.Sign(params, secret)
	}
	if !headerAuth {
		form.Set("sign", signature)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerAuth {
		r.Header.Set("Api-Id", key)
		r.Header.Set("Api-Signature", signature)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var reply map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("invalid protocol response %d %s: %v", w.Code, w.Body, err)
	}
	return reply
}

func TestMCYOpenAPIAndOfficialV3Plugin(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	account := seedCompatAccount(t, repo, "mcy_shop", "MCY-1", "secret", 10000)
	ctx := context.Background()
	// All protocol surfaces must quote and charge this account's supply price.
	if _, err := repo.UpsertPrice(ctx, account.ID, 1, 0, 850); err != nil {
		t.Fatal(err)
	}
	native, legacy := mcyMux(svc), acgMux(svc)
	call := func(path string, form url.Values) map[string]any {
		return mcyCall(t, native, "/plugin/open-api/"+path, "MCY-1", "secret", form, true)
	}
	ping := call("connect", nil)
	if ping["code"] != float64(200) || ping["data"].(map[string]any)["balance"] != "100.00" {
		t.Fatal(ping)
	}
	list := call("items", nil)
	items := list["data"].([]any)
	if len(items) != 1 {
		t.Fatal("inactive product exposed", list)
	}
	item := items[0].(map[string]any)
	sku := item["sku"].([]any)[0].(map[string]any)
	if item["id"] != float64(1) || item["widget"] != "[]" || sku["id"] != float64(1) || sku["stock_price"] != "8.50" || sku["stock"] != float64(5) {
		t.Fatal("MCY catalog shape differs from client contract", list)
	}
	for _, tc := range []struct {
		path  string
		form  url.Values
		field string
		value any
	}{
		{"item", url.Values{"id": {"1"}}, "id", float64(1)},
		{"sku/stock", url.Values{"sku_id": {"1"}}, "stock", float64(5)},
		{"sku/state", url.Values{"sku_id": {"1"}, "quantity": {"6"}}, "state", false},
		{"amount", url.Values{"sku_id": {"1"}, "quantity": {"2"}}, "amount", "17.00"},
	} {
		reply := call(tc.path, tc.form)
		if reply["code"] != float64(200) || reply["data"].(map[string]any)[tc.field] != tc.value {
			t.Fatal(tc.path, reply)
		}
	}
	form := url.Values{"sku_id": {"1"}, "quantity": {"2"}, "trade_no": {"original-24-character-no"}}
	for i := 0; i < 2; i++ {
		reply := call("trade", form)
		if reply["code"] != float64(200) || reply["data"].(map[string]any)["contents"] != "CARD-100\nCARD-101" {
			t.Fatal("native purchase/replay failed", reply)
		}
	}
	balance, _ := repo.BalanceOf(ctx, account.ID)
	if balance != 8300 {
		t.Fatal("replay charged twice", balance)
	}
	conflict := call("trade", url.Values{"sku_id": {"1"}, "quantity": {"1"}, "trade_no": {"original-24-character-no"}})
	if conflict["code"] == float64(200) {
		t.Fatal("idempotency conflict accepted")
	}
	// The official MCYShopV3 plugin calls inventory + inventoryState, not stock.
	legacyList := mcyCall(t, legacy, "/shared/commodity/items", "MCY-1", "secret", nil, false)
	for _, rawCategory := range legacyList["data"].([]any) {
		for _, rawProduct := range rawCategory.(map[string]any)["children"].([]any) {
			product := rawProduct.(map[string]any)
			if product["status"] != float64(1) || product["factory_price"] != "8.50" {
				t.Fatal("MCY plugin received inactive product or wrong purchase cost", product)
			}
		}
	}
	inv := mcyCall(t, legacy, "/shared/commodity/inventory", "MCY-1", "secret", url.Values{"sharedCode": {"1"}}, false)
	if inv["code"] != float64(200) || inv["data"].(map[string]any)["count"] != float64(5) || inv["data"].(map[string]any)["factory_price"] != "8.50" {
		t.Fatal("official plugin inventory incompatible", inv)
	}
	state := mcyCall(t, legacy, "/shared/commodity/inventoryState", "MCY-1", "secret", url.Values{"shared_code": {"1"}, "num": {"6"}}, false)
	if state["code"] == float64(200) {
		t.Fatal("official plugin shortage accepted", state)
	}
	for i := 0; i < 2; i++ {
		trade := mcyCall(t, legacy, "/shared/commodity/trade", "MCY-1", "secret", url.Values{"shared_code": {"1"}, "num": {"1"}, "request_no": {"mcy-plugin-order"}}, false)
		if trade["code"] != float64(200) || trade["data"].(map[string]any)["secret"] != "CARD-100" {
			t.Fatal("official plugin trade/replay incompatible", trade)
		}
	}
	balance, _ = repo.BalanceOf(ctx, account.ID)
	if balance != 7450 {
		t.Fatal("legacy plugin replay charged twice", balance)
	}
}

func TestMCYAuthenticationAndSKUProtection(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	acc := seedCompatAccount(t, repo, "mcy_shop", "mcy", "secret", 0)
	seedCompatAccount(t, repo, "acg_faka", "other-protocol", "secret", 10000)
	handler := mcyMux(svc)
	for _, pair := range [][2]string{{"mcy", "wrong"}, {"other-protocol", "secret"}, {"unknown", "secret"}, {"", ""}} {
		reply := mcyCall(t, handler, "/plugin/open-api/connect", pair[0], pair[1], nil, true)
		if reply["code"] == float64(200) {
			t.Fatal("invalid identity accepted", reply)
		}
	}
	for _, form := range []url.Values{{"sku_id": {"1", "2"}}, {"sku_id[]": {"1"}}} {
		reply := mcyCall(t, handler, "/plugin/open-api/sku/stock", "mcy", "secret", form, true)
		if reply["code"] == float64(200) {
			t.Fatal("ambiguous/unsigned form accepted")
		}
	}
	poor := mcyCall(t, handler, "/plugin/open-api/trade", "mcy", "secret", url.Values{"sku_id": {"1"}, "quantity": {"1"}, "trade_no": {"poor"}}, true)
	if poor["code"] == float64(200) || repo.data.Client.SupplyOrder.Query().CountX(context.Background()) != 0 {
		t.Fatal("insufficient balance did not roll back", poor)
	}
	ctx := context.Background()
	repo.data.Client.ProductSku.Create().SetProductID(1).SetName("real-sku").SetSpecValues(map[string]string{}).SetPrice(1000).SaveX(ctx)
	detail := mcyCall(t, handler, "/plugin/open-api/item", "mcy", "secret", url.Values{"id": {"1"}}, true)
	if detail["code"] == float64(200) {
		t.Fatal("native MCY exposed unsupported local SKU product")
	}
	if err := repo.SetIPWhitelist(ctx, acc.ID, []string{"203.0.113.7"}); err != nil {
		t.Fatal(err)
	}
	denied := mcyCall(t, handler, "/plugin/open-api/connect", "mcy", "secret", nil, true)
	if denied["code"] == float64(200) {
		t.Fatal("IP whitelist ignored")
	}
}

func TestMCYRegistrationAndAdminAccount(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	admin := NewAdminSupplierService(repo, svc)
	if _, err := admin.CreateAccount(context.Background(), &adminv1.CreateSupplierAccountRequest{Name: "MCY", Protocol: "mcy_shop", ApiKey: "new", ApiSecret: "secret"}); err != nil {
		t.Fatal("MCY admin account unavailable", err)
	}
	// Frontend validation must accept MCY before enforcing the required name.
	store := NewStoreSupplierService(repo, nil, nil)
	_, err := store.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{Protocol: "mcy_shop"})
	if err == nil || strings.Contains(err.Error(), "INVALID_PROTOCOL") {
		t.Fatal("MCY storefront application unavailable", err)
	}
	server := httptest.NewServer(mcyMux(svc))
	defer server.Close()
	response, err := http.Get(server.URL + "/plugin/open-api/connect")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal(fmt.Sprint("GET method accepted: ", string(body)))
	}
}
