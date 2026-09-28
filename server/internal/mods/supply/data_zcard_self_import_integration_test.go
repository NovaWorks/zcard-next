//go:build integration

package supply

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type selfImportCatalog struct{ catalogport.SupplierCatalog }

func (selfImportCatalog) ListForSupply(context.Context, catalogport.AdminFilter) ([]catalogport.SupplierProduct, int64, error) {
	return []catalogport.SupplierProduct{{ID: 17, Name: "自对接商品", Price: 1234, CategoryID: 7, Status: 1}}, 1, nil
}
func (selfImportCatalog) GetForSupply(context.Context, uint64) (*catalogport.SupplierProduct, error) {
	return &catalogport.SupplierProduct{ID: 17, Name: "自对接商品", Price: 1234, Status: 1}, nil
}

type selfImportInventory struct{ invport.Inventory }

func (selfImportInventory) Stock(context.Context, uint64, uint64) (int64, error) {
	return 8, nil
}

// Run in a separate process with ZCARD_HTTPX_ALLOW_PRIVATE=1. All accounts,
// balances, products and imports belong to an isolated in-memory test database.
func TestZCardSelfImportHTTP(t *testing.T) {
	if os.Getenv("ZCARD_HTTPX_ALLOW_PRIVATE") != "1" {
		t.Skip("requires explicit loopback test mode")
	}
	ctx := context.Background()
	repo, d := newTestRepo(t)
	upstream := supplier.NewSupplierRepoImpl(d, newTestBox(t))
	acc, err := upstream.CreateAccount(ctx, "self import", "self-key", "self-secret", "", "zcard", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upstream.ReviewAccount(ctx, acc.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := upstream.Recharge(ctx, acc.ID, 12345, "test-recharge", ""); err != nil {
		t.Fatal(err)
	}
	api := supplier.NewSupplyAPIService(upstream, selfImportCatalog{}, selfImportInventory{}, nil, nil, nil, slog.Default())
	server := khttp.NewServer(khttp.Filter(supplier.SupplyAuthFilter(upstream, 0)))
	supplyv1.RegisterSupplyServiceHTTPServer(server, api)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	writer := catalog.NewProductRepoImpl(d, nil)
	syncSvc := NewSyncService(repo, writer, writer, nil, nil, nil, slog.Default())
	svc := NewAdminSupplyService(repo, syncSvc)
	conn, err := svc.CreateConnection(ctx, &adminv1.CreateConnectionRequest{
		Name: "self", Driver: "zcard", BaseUrl: httpServer.URL + "/api/supply",
		Credentials: `{"api_key":"self-key","api_secret":"self-secret"}`, ExchangeRate: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{httpServer.URL, httpServer.URL + "/api/supply/"} {
		if _, err := svc.UpdateConnection(ctx, &adminv1.UpdateConnectionRequest{Id: conn.Id, BaseUrl: base}); err != nil {
			t.Fatal(err)
		}
		ping, err := svc.PingConnection(ctx, &adminv1.PingConnectionRequest{Id: conn.Id})
		if err != nil || !ping.Ok || ping.Balance != 12345 {
			t.Fatalf("ping after URL edit: %v %v", ping, err)
		}
	}
	snapshot, err := svc.ensureCatalogSnapshot(ctx, conn.Id, true)
	if err != nil {
		t.Fatal(err)
	}
	svc.runCatalogSnapshot(snapshot.ID)
	awaitCatalog(t, svc, snapshot.ID, "ready")
	result, err := svc.ImportProducts(ctx, &adminv1.ImportProductsRequest{
		ConnectionId: conn.Id, SnapshotId: snapshot.Token, Codes: []string{"17"}, PricingMode: PriceModeEqual, RequestKey: "self-import",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		task, err := repo.GetSyncTask(ctx, result.Task.Id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "done" {
			stats, err := svc.importTaskProto(ctx, task)
			if err != nil || stats.Created != 1 || stats.FailedCount != 0 || stats.StockPendingCount != 0 {
				t.Fatalf("import result: %v %v", stats, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("import did not complete: %s %s", task.Status, task.ErrorContext)
		}
		time.Sleep(10 * time.Millisecond)
	}
	saved := d.Client.Product.Query().Where(product.UpstreamSourceID(conn.Id), product.UpstreamProductCode("17")).OnlyX(ctx)
	if saved.Name != "自对接商品" || saved.Price != 1234 || saved.Status != 1 {
		t.Fatalf("imported product fields incorrect: name=%s price=%d status=%d", saved.Name, saved.Price, saved.Status)
	}
	mapping, err := repo.GetMapping(ctx, conn.Id, "17", "")
	if err != nil || mapping.LocalProductID != saved.ID || mapping.UpStock != 8 {
		t.Fatalf("imported mapping/stock incorrect: %v %v", mapping, err)
	}
}
