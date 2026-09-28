package supplier

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func supplyBusinessSnapshot(t *testing.T, repo *SupplierRepoImpl) string {
	t.Helper()
	var snapshot []any
	for _, table := range []string{"cards", "supply_orders", "supplier_ledger_entries", "downstream_callbacks", "orders", "procurement_orders", "outbox_events"} {
		rows, err := repo.data.DB.Query("SELECT * FROM " + table + " ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var values [][]any
		for rows.Next() {
			v := make([]any, len(cols))
			args := make([]any, len(cols))
			for i := range v {
				args[i] = &v[i]
			}
			if err = rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		snapshot = append(snapshot, values)
	}
	b, _ := json.Marshal(snapshot)
	return string(b)
}
func TestP2SupplyHTTPGates(t *testing.T) {
	for _, protocol := range []string{"zcard", "acg_faka", "dujiao_next"} {
		t.Run(protocol, func(t *testing.T) {
			svc, repo, _ := newCompatEnv(t)
			securityStock(t, svc, 4)
			account := seedCompatAccount(t, repo, protocol, "p2-key", "p2-secret", 10000)
			// Seed an existing delivery before turning on the host requirement.
			no := "existing"
			if protocol == "acg_faka" {
				no = acgOrderPrefix + no
			}
			original, err := svc.fulfillOrder(context.Background(), account.ID, 1, 1, no, "", "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = repo.data.Client.PluginRequirement.Create().SetPluginID("member-purchase-gate").SetSubsiteID(0).SetProductID(1).SetRequired(true).SetRevision(1).Save(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			// No runtime is installed: supply rejection must depend on durable requirements.
			standard := khttp.NewServer(khttp.Filter(SupplyAuthFilter(repo, 0)))
			supplyv1.RegisterSupplyServiceHTTPServer(standard, svc)
			var handler http.Handler = standard
			if protocol == "acg_faka" {
				handler = acgMux(svc)
			}
			if protocol == "dujiao_next" {
				handler = dujiaoMux(svc)
			}
			call := func(number string) (int, string) {
				var path, body string
				switch protocol {
				case "zcard":
					path = "/api/supply/orders"
					body = fmt.Sprintf(`{"product_id":"1","quantity":1,"downstream_order_no":%q,"callback_url":"https://example.test/callback"}`, number)
				case "dujiao_next":
					path = "/api/v1/upstream/orders"
					body = fmt.Sprintf(`{"sku_id":1,"quantity":1,"downstream_order_no":%q}`, number)
				case "acg_faka":
					path = "/shared/commodity/trade"
					f := url.Values{"app_id": {"p2-key"}, "app_key": {"p2-secret"}, "shared_code": {"1"}, "num": {"1"}, "request_no": {number}}
					f.Set("sign", acgTestSign(f, "p2-secret"))
					body = f.Encode()
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				switch protocol {
				case "zcard":
					req.Header.Set("X-Supply-Key", account.APIKey)
					req.Header.Set("X-Supply-Timestamp", ts)
					req.Header.Set("X-Supply-Nonce", number)
					req.Header.Set("X-Supply-Signature", supplySign("p2-secret", "POST", path, "", ts, number, []byte(body)))
				case "dujiao_next":
					req.Header.Set("Dujiao-Next-Api-Key", account.APIKey)
					req.Header.Set("Dujiao-Next-Timestamp", ts)
					req.Header.Set("Dujiao-Next-Signature", dujiaoSign("p2-secret", "POST", path, ts, []byte(body)))
				case "acg_faka":
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				return w.Code, w.Body.String()
			}
			before := supplyBusinessSnapshot(t, repo)
			balance, _ := repo.BalanceOf(context.Background(), account.ID)
			status, body := call("new-required")
			if !strings.Contains(body, "SUPPLY_RESTRICTED") || strings.Contains(body, "SECRET-") {
				t.Fatalf("missing safe protocol denial: %d %s", status, body)
			}
			if protocol == "dujiao_next" && status != 403 {
				t.Fatal(status)
			}
			afterBalance, _ := repo.BalanceOf(context.Background(), account.ID)
			if before != supplyBusinessSnapshot(t, repo) || balance != afterBalance {
				t.Fatal("denial changed business state")
			}
			status, body = call("existing")
			expected := original.cards[0]
			if protocol == "dujiao_next" {
				expected = "delivered"
			}
			if status != 200 || !strings.Contains(body, expected) {
				t.Fatalf("existing delivery blocked: %d %s", status, body)
			}
		})
	}
}

func TestP2SupplyDatabaseGate(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_P2_SUPPLY_" + strings.ToUpper(driver) + "_DSN")
			if source == "" {
				t.Skip("isolated P2 supply database not configured")
			}
			d, closeDB, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer closeDB()
			ctx := context.Background()
			if err = d.Client.Schema.Create(ctx); err != nil {
				t.Fatal(err)
			}
			box, err := crypto.NewBox(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			repo := NewSupplierRepoImpl(d, box)
			cat := &fakeCatalog{prods: []catalogport.SupplierProduct{{ID: 1, Name: "P2 supply", Price: 1000, Status: 1}}}
			svc := &SupplyAPIService{repo: repo, reader: cat}
			securityStock(t, svc, 3)
			account := seedCompatAccount(t, repo, "zcard", "p2-db", "secret", 10000)
			old, err := svc.fulfillOrder(ctx, account.ID, 1, 1, "old", "", "")
			if err != nil {
				t.Fatal(err)
			}
			d.Client.PluginRequirement.Create().SetPluginID("member-purchase-gate").SetProductID(1).SetSubsiteID(0).SetRequired(true).SetRevision(1).SaveX(ctx)
			before := supplyBusinessSnapshot(t, repo)
			out, err := svc.fulfillOrder(ctx, account.ID, 1, 1, "new", "https://example.test/callback", "")
			if err != nil || out == nil || !out.rejected || out.errCode != "SUPPLY_RESTRICTED" {
				t.Fatalf("gate: %+v %v", out, err)
			}
			if before != supplyBusinessSnapshot(t, repo) {
				t.Fatal("supply denial changed data")
			}
			replay, err := svc.fulfillOrder(ctx, account.ID, 1, 1, "old", "", "")
			if err != nil || replay.order.ID != old.order.ID || replay.cards[0] != old.cards[0] {
				t.Fatal("legal replay denied", err)
			}
		})
	}
}
