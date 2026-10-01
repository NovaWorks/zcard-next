//go:build integration

package supply

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// The producer fixture comes from zcard-market's real import + Supply API,
// rather than inventing an upstream channel DTO independently in each repo.
func TestSMSPrivateProviderCatalogImportsOneProduct(t *testing.T) {
	path := os.Getenv("ZCARD_SMS_CONTRACT_FILE")
	if path == "" || os.Getenv("ZCARD_HTTPX_ALLOW_PRIVATE") != "1" {
		t.Skip("requires isolated private-provider wire fixture")
	}
	response, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Items []json.RawMessage `json:"items"`
	}
	if err = json.Unmarshal(response, &wire); err != nil || len(wire.Items) != 1 {
		t.Fatalf("provider did not publish one product: %s %v", response, err)
	}
	var entry struct {
		ID         string `json:"id"`
		Kind       string `json:"product_kind"`
		CategoryID string `json:"category_id"`
	}
	if err = json.Unmarshal(wire.Items[0], &entry); err != nil || entry.Kind != "sms_channel" {
		t.Fatal("missing service identity", err)
	}
	stockCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Supply-Signature") != adapter.ZCardSign("secret", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Supply-Timestamp"), r.Header.Get("X-Supply-Nonce"), body) {
			t.Error("invalid signed request")
		}
		switch r.URL.Path {
		case "/api/supply/ping":
			fmt.Fprint(w, `{"ok":true,"currency":"CNY","balance":"10000","capabilities":["sms_activation.v1","sms_polling.v1","sms_channel_catalog.v1","sms_channel_purchase.v1"]}`)
		case "/api/supply/categories":
			fmt.Fprintf(w, `{"categories":[{"id":%q,"name":"短信接码"}]}`, entry.CategoryID)
		case "/api/supply/products":
			if !strings.Contains(r.URL.Query().Get("capabilities"), "sms_channel_catalog.v1") {
				t.Error("collector did not negotiate channel catalog")
			}
			w.Write(response)
		case "/api/supply/products/" + entry.ID:
			fmt.Fprintf(w, `{"product":%s}`, wire.Items[0])
		case "/api/supply/products/" + entry.ID + "/stock":
			stockCalls++
			t.Error("channel requested aggregate stock")
			w.WriteHeader(500)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	repo, d := newTestRepo(t)
	writer := catalog.NewProductRepoImpl(d, nil)
	syncSvc := NewSyncService(repo, writer, writer, nil, nil, nil, slog.Default())
	svc := NewAdminSupplyService(repo, syncSvc)
	conn, err := svc.CreateConnection(ctx, &adminv1.CreateConnectionRequest{Name: "Private channel provider", Driver: "zcard", BaseUrl: srv.URL, Credentials: `{"api_key":"key","api_secret":"secret"}`, ExchangeRate: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i == 0 {
			d.Client.Product.Create().SetName("Collected legacy offer").SetSlug("collected-legacy").SetDeliveryKind("sms_activation").SetUpstreamSourceID(conn.Id).SetUpstreamProductCode("old-offer").SetPrice(100).SetStatus(1).SaveX(ctx)
		}
		preview, err := svc.PreviewProducts(ctx, &adminv1.PreviewProductsRequest{ConnectionId: conn.Id, Page: 1, PageSize: 50})
		if err != nil || preview.Total != 1 || len(preview.Categories) != 1 || preview.Categories[0].Products[0].ProductKind != "sms_channel" {
			t.Fatalf("client preview split products: %+v %v", preview, err)
		}
		reply, err := svc.ImportProducts(ctx, &adminv1.ImportProductsRequest{ConnectionId: conn.Id, SnapshotId: preview.SnapshotId, Codes: []string{entry.ID}, PricingMode: PriceModeFixed, MarkupAmountCents: int64(200 + i*100), RequestKey: fmt.Sprintf("provider-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			task, err := repo.GetSyncTask(ctx, reply.Task.Id)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status == "done" {
				stats, err := svc.importTaskProto(ctx, task)
				if err != nil || stats.FailedCount != 0 || stats.ManualSkipped != 0 || (i == 0 && stats.Created != 1) || (i == 1 && stats.Updated != 1) {
					t.Fatalf("import did not save rule: %+v %v", stats, err)
				}
				break
			}
			if task.Status == "failed" || time.Now().After(deadline) {
				t.Fatalf("client import: %+v", task)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if d.Client.Product.Query().Where(product.StatusGTE(0)).CountX(ctx) != 1 || d.Client.ProductSku.Query().CountX(ctx) != 0 || stockCalls != 0 || d.Client.Product.Query().Where(product.UpstreamProductCode("old-offer"), product.Status(-1)).CountX(ctx) != 1 {
		t.Fatal("client materialized dynamic offers or requested channel stock")
	}
	p := d.Client.Product.Query().Where(product.StatusGTE(0)).OnlyX(ctx)
	if p.ProductKind != "sms_channel" || p.Price != 0 || p.Status != 1 {
		t.Fatalf("invalid client service: %+v", p)
	}
	m, err := repo.GetMapping(ctx, conn.Id, entry.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := readProductRule(m.PricingOverride)
	if err != nil || rule.Amount != 300 {
		t.Fatal("retail rule lost", err)
	}
}
