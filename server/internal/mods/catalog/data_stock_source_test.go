package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/dashboard"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestStockSources(t *testing.T) {
	d, admin := newStatsEnv(t)
	ctx := context.Background()
	repo := NewProductRepoImpl(d, nil)
	store := NewStoreCatalogService(NewCatalogUsecase(repo), nil, nil)
	expected := map[uint64]int64{}
	makeProduct := func(name, kind string, source uint64, stock int32, cards int) *ent.Product {
		q := d.Client.Product.Create().SetName(name).SetSlug(name).SetPrice(100).SetStockType(product.StockType(kind)).SetStatus(1).SetStockVisible(true)
		if source > 0 {
			q.SetUpstreamSourceID(source).SetUpstreamProductCode(name)
		}
		p := q.SaveX(ctx)
		if source > 0 && stock != -2 {
			d.Client.SupplyMapping.Create().SetConnectionID(source).SetUpstreamProduct(name).SetLocalProductID(p.ID).SetUpStock(stock).SetStockCheckedAt(time.Now().UTC()).SaveX(ctx)
		}
		for i := 0; i < cards; i++ {
			d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("%s-%d", name, i)).SaveX(ctx)
		}
		expected[p.ID] = int64(stock)
		return p
	}
	local0 := makeProduct("local-zero", "card", 0, 0, 0)
	local3 := makeProduct("local-three", "card", 0, 3, 3)
	makeProduct("local-many", "card", 0, 12, 12)
	up0 := makeProduct("up-zero", "card", 9, 0, 8) // Returned/old local cards must never inflate upstream stock.
	up129 := makeProduct("up-129", "card", 9, 129, 0)
	makeProduct("up-299", "card", 9, 299, 0)
	makeProduct("up-unlimited", "card", 9, -1, 0)
	missing := makeProduct("up-unknown", "card", 9, -2, 6)
	direct := makeProduct("direct", "url", 0, -1, 0)
	makeProduct("direct-code", "code", 0, -1, 0)
	makeProduct("up-url-zero", "url", 9, 0, 0) // Upstream source has priority over local direct type.
	// Wrong source mapping and SKU-level rows must not replace product-level stock.
	d.Client.SupplyMapping.Create().SetConnectionID(8).SetUpstreamProduct(missing.Name).SetLocalProductID(missing.ID).SetUpStock(999).SaveX(ctx)
	d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct(up129.Name).SetLocalProductID(up129.ID).SetUpstreamSku("sku-zero").SetUpStock(0).SaveX(ctx)
	d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct("old-direct").SetLocalProductID(direct.ID).SetUpStock(0).SaveX(ctx)
	// Sold/reserved cards and incorrectly scoped cards cannot become local available stock.
	for i, st := range []card.Status{card.StatusUsed, card.StatusReserved} {
		d.Client.Card.Create().SetProductID(local3.ID).SetStatus(st).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("unavailable-%d", i)).SaveX(ctx)
	}
	d.Client.Card.Create().SetProductID(local0.ID).SetSubsiteID(7).SetContent([]byte("x")).SetContentHash("wrong-subsite").SaveX(ctx)
	sub := d.Client.Product.Create().SetSubsiteID(7).SetName("other-site").SetSlug("other-site").SetStatus(1).SaveX(ctx)
	for id, want := range expected {
		detail, err := store.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		if detail.Stock != want {
			t.Errorf("detail %s stock=%d want=%d", detail.Name, detail.Stock, want)
		}
	}
	front, err := store.ListProducts(ctx, &storefrontv1.ListProductsRequest{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range front.Items {
		if n, ok := expected[p.Id]; !ok || n != p.Stock {
			t.Errorf("front stock %s=%d", p.Name, p.Stock)
		}
	}
	all, err := admin.ListProducts(ctx, &adminv1.ListProductsRequest{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Products) != len(expected) {
		t.Fatal("tenant leaked or products missing")
	}
	for _, p := range all.Products {
		if p.Stock != expected[p.Id] {
			t.Errorf("admin stock %s=%d want=%d", p.Name, p.Stock, expected[p.Id])
		}
	}
	for _, tc := range []struct {
		name string
		req  *adminv1.ListProductsRequest
		want int64
	}{
		{"low", &adminv1.ListProductsRequest{LowStockOnly: true}, 4},
		{"zero", &adminv1.ListProductsRequest{OutOfStockOnly: true}, 3},
		{"local-zero", &adminv1.ListProductsRequest{OutOfStockOnly: true, LocalOnly: true}, 1},
		{"upstream-zero", &adminv1.ListProductsRequest{OutOfStockOnly: true, UpstreamSourceId: 9}, 2},
		{"search", &adminv1.ListProductsRequest{OutOfStockOnly: true, Keyword: "up-zero"}, 1},
		{"pagination", &adminv1.ListProductsRequest{OutOfStockOnly: true, Page: 2, PageSize: 1}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply, err := admin.ListProducts(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if reply.Total != tc.want {
				t.Fatalf("total=%d want=%d", reply.Total, tc.want)
			}
			for _, p := range reply.Products {
				if p.Stock < 0 || p.Stock >= 10 || (tc.req.OutOfStockOnly && p.Stock != 0) {
					t.Fatalf("wrong filtered stock: %s=%d", p.Name, p.Stock)
				}
			}
			if tc.req.Page == 2 && len(reply.Products) != 1 {
				t.Fatal("pagination broken")
			}
		})
	}
	count, err := dashboard.NewDashboardRepoImpl(d).GetLowStockCount(ctx, 10)
	if err != nil || count != 4 {
		t.Fatalf("dashboard=%d err=%v", count, err)
	}
	subCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 7})
	stocks, err := repo.StockBatch(subCtx, []uint64{sub.ID, up0.ID})
	if err != nil || len(stocks) != 1 || stocks[sub.ID] != 0 {
		t.Fatalf("subsite stocks=%v err=%v", stocks, err)
	}
}
