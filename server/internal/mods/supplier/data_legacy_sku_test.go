package supplier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestLegacyProtocolsRejectNewSKUOrdersButReplayOldReceipts(t *testing.T) {
	for _, protocol := range []string{"zcard", "dujiao_next", "acg_faka"} {
		t.Run(protocol, func(t *testing.T) {
			svc, repo, _ := newCompatEnv(t)
			securityStock(t, svc, 4)
			ctx := context.Background()
			account := seedCompatAccount(t, repo, protocol, "sku-"+protocol, "sku-secret", 10000)
			var handler http.Handler
			switch protocol {
			case "zcard":
				server := khttp.NewServer(khttp.Filter(SupplyAuthFilter(repo, 0)))
				supplyv1.RegisterSupplyServiceHTTPServer(server, svc)
				handler = server
			case "dujiao_next":
				handler = dujiaoMux(svc)
			case "acg_faka":
				handler = acgMux(svc)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			nonce := 0
			call := func(method, path, body string) (int, string) {
				t.Helper()
				nonce++
				contentType := "application/json"
				if protocol == "acg_faka" {
					form, err := url.ParseQuery(body)
					if err != nil {
						t.Fatal(err)
					}
					form.Set("app_id", account.APIKey)
					form.Set("app_key", "sku-secret")
					form.Set("sign", acgTestSign(form, "sku-secret"))
					body, contentType = form.Encode(), "application/x-www-form-urlencoded"
				}
				r, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
				r.Header.Set("Content-Type", contentType)
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				if protocol == "zcard" {
					n := strconv.Itoa(nonce)
					r.Header.Set("X-Supply-Key", account.APIKey)
					r.Header.Set("X-Supply-Timestamp", ts)
					r.Header.Set("X-Supply-Nonce", n)
					r.Header.Set("X-Supply-Signature", supplySign("sku-secret", method, path, "", ts, n, []byte(body)))
				} else if protocol == "dujiao_next" {
					r.Header.Set("Dujiao-Next-Api-Key", account.APIKey)
					r.Header.Set("Dujiao-Next-Timestamp", ts)
					r.Header.Set("Dujiao-Next-Signature", dujiaoSign("sku-secret", method, path, ts, []byte(body)))
				}
				response, err := server.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				raw, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				return response.StatusCode, string(raw)
			}
			orderPath := "/api/supply/orders"
			bodyFor := func(no string) string { return `{"product_id":"1","quantity":1,"downstream_order_no":"` + no + `"}` }
			orderNo := "sku-before"
			if protocol == "dujiao_next" {
				orderPath = "/api/v1/upstream/orders"
				bodyFor = func(no string) string { return `{"sku_id":1,"quantity":1,"downstream_order_no":"` + no + `"}` }
			} else if protocol == "acg_faka" {
				orderPath = "/shared/commodity/trade"
				bodyFor = func(no string) string {
					return url.Values{"shared_code": {"1"}, "num": {"1"}, "request_no": {no}}.Encode()
				}
				orderNo = acgOrderPrefix + orderNo
			}
			code, first := call(http.MethodPost, orderPath, bodyFor("sku-before"))
			if code != 200 || (protocol != "dujiao_next" && !strings.Contains(first, "SECRET-0")) || (protocol == "dujiao_next" && !strings.Contains(first, `"status":"delivered"`)) {
				t.Fatalf("initial receipt failed: status=%d response=%s", code, first)
			}
			original, err := repo.GetSupplyOrderByNo(ctx, account.ID, orderNo)
			if err != nil {
				t.Fatal(err)
			}
			repo.data.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(1).SetName("new month SKU").SaveX(ctx)
			// Catalog reader remains deliberately stale; database eligibility wins.
			code, rejected := call(http.MethodPost, orderPath, bodyFor("sku-after"))
			if strings.Contains(rejected, "SECRET-") {
				t.Fatal("new SKU order received a default-pool card")
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(rejected), &decoded); err != nil {
				t.Fatal(err)
			}
			switch protocol {
			case "zcard":
				if code != 200 || decoded["status"] != "rejected" || decoded["error_code"] != "product_unavailable" {
					t.Fatalf("native rejection: %s", rejected)
				}
			case "dujiao_next":
				if code != 400 || decoded["ok"] != false || decoded["error_code"] != "product_unavailable" {
					t.Fatalf("dujiao rejection: %s", rejected)
				}
			case "acg_faka":
				if code != 200 || decoded["code"] != float64(0) {
					t.Fatalf("acg rejection: %s", rejected)
				}
			}
			code, replay := call(http.MethodPost, orderPath, bodyFor("sku-before"))
			if code != 200 || (protocol != "dujiao_next" && !strings.Contains(replay, "SECRET-0")) || (protocol == "dujiao_next" && !strings.Contains(replay, `"status":"delivered"`)) {
				t.Fatalf("old order replay lost receipt: %d %s", code, replay)
			}
			current, err := repo.GetSupplyOrderByNo(ctx, account.ID, orderNo)
			if err != nil || current.ID != original.ID {
				t.Fatal("replay replaced the original order")
			}
			if balance, err := repo.BalanceOf(ctx, account.ID); err != nil || balance != 9000 {
				t.Fatalf("rejected request/replay changed balance: %d %v", balance, err)
			}
			if repo.data.Client.SupplyOrder.Query().CountX(ctx) != 1 || repo.data.Client.Card.Query().Where(card.StatusEQ(card.StatusUsed)).CountX(ctx) != 1 {
				t.Fatal("rejection/replay created an order or consumed another card")
			}
			queryPath := "/api/supply/orders/" + strconv.FormatUint(original.ID, 10)
			queryMethod, queryBody := http.MethodGet, ""
			if protocol == "dujiao_next" {
				queryPath = "/api/v1/upstream/orders/" + strconv.FormatUint(original.ID, 10)
			}
			if protocol == "acg_faka" {
				queryPath, queryMethod, queryBody = "/shared/commodity/query", http.MethodPost, "tradeNo="+strconv.FormatUint(original.ID, 10)
			}
			if status, body := call(queryMethod, queryPath, queryBody); status != 200 || !strings.Contains(body, "SECRET-0") {
				t.Fatalf("old order query lost receipt after SKU added: %d %s", status, body)
			}
			if protocol == "zcard" {
				for _, path := range []string{"/api/supply/products/1", "/api/supply/products/1/stock"} {
					if code, body := call(http.MethodGet, path, ""); code != 404 || strings.Contains(body, "SECRET-") {
						t.Fatalf("native detail/stock advertised SKU product: %d %s", code, body)
					}
				}
			} else if protocol == "dujiao_next" {
				if code, body := call(http.MethodGet, "/api/v1/upstream/products/1", ""); code != 404 {
					t.Fatalf("dujiao detail advertised SKU product: %d %s", code, body)
				}
			} else {
				for _, path := range []string{"/shared/commodity/item", "/shared/commodity/stock", "/shared/commodity/valuation"} {
					_, body := call(http.MethodPost, path, "code=1")
					var out map[string]any
					if err := json.Unmarshal([]byte(body), &out); err != nil || out["code"] != float64(0) {
						t.Fatalf("acg detail/stock/quote advertised SKU product: %s %v", body, err)
					}
				}
			}
		})
	}
}
