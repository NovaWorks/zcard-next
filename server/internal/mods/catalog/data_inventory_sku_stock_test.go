package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestSKUAvailableStockExcludesUnassignedAndDeletedPools(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("scoped pools").SetSlug("scoped-pools").SetStatus(1).SaveX(ctx)
	a := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName("month").SaveX(ctx)
	b := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName("year").SaveX(ctx)
	for i, pool := range []struct {
		sku, tenant uint64
		status      card.Status
	}{
		{a.ID, 0, card.StatusAvailable}, {a.ID, 0, card.StatusAvailable}, {b.ID, 0, card.StatusAvailable},
		{0, 0, card.StatusAvailable}, {0, 0, card.StatusAvailable}, {9999, 0, card.StatusAvailable},
		{a.ID, 8, card.StatusAvailable}, {a.ID, 0, card.StatusReserved}, {b.ID, 0, card.StatusDisabled},
	} {
		q := d.Client.Card.Create().SetProductID(p.ID).SetSubsiteID(pool.tenant).SetContent([]byte("encrypted-fixture")).SetContentHash(fmt.Sprint(i)).SetStatus(pool.status)
		if i != 3 {
			q.SetSkuID(pool.sku)
		} // Cover both historical NULL and explicit 0.
		q.SaveX(ctx)
	}
	stocks, err := data.ProductStocks(ctx, d, []*ent.Product{p})
	if err != nil || stocks[p.ID] != 3 {
		t.Fatalf("SKU stock counted unrelated pools: %v %v", stocks, err)
	}
	d.Client.Card.Delete().Where(card.SkuIDIn(a.ID, b.ID)).ExecX(ctx)
	stocks, err = data.ProductStocks(ctx, d, []*ent.Product{p})
	if err != nil || stocks[p.ID] != 0 {
		t.Fatalf("unassigned cards made empty SKUs appear available: %v %v", stocks, err)
	}
}

func TestMixedSKUStockCountsManualQuotaOnce(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("mixed pools").SetSlug("mixed-pools").SetStatus(1).SetManualStock(5).SaveX(ctx)
	a := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName("local").SetFulfillmentMode("local").SaveX(ctx)
	for _, name := range []string{"manual-a", "manual-b"} {
		d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName(name).SetFulfillmentMode("manual").SaveX(ctx)
	}
	for i := 0; i < 4; i++ {
		d.Client.Card.Create().SetProductID(p.ID).SetSkuID(a.ID).SetContent([]byte("x")).SetContentHash(fmt.Sprint(i)).SaveX(ctx)
	}
	d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("x")).SetContentHash("legacy").SaveX(ctx)
	stocks, err := data.ProductStocks(ctx, d, []*ent.Product{p})
	if err != nil || stocks[p.ID] != 9 {
		t.Fatalf("shared manual quota duplicated or default pool counted: %v %v", stocks, err)
	}
	p = d.Client.Product.UpdateOne(p).SetManualStock(-1).SaveX(ctx)
	stocks, err = data.ProductStocks(ctx, d, []*ent.Product{p})
	if err != nil || stocks[p.ID] != -1 {
		t.Fatalf("unlimited manual quota lost: %v %v", stocks, err)
	}
}

func TestSKUReuseStockKeepsPauseExpiryAndUnlimited(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("reuse stock").SetSlug("reuse-stock").SetStatus(1).SaveX(ctx)
	sk := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName("reusable").SetFulfillmentMode("reuse").SaveX(ctx)
	source := d.Client.ProductDeliverySource.Create().SetProductID(p.ID).SetSkuID(sk.ID).SetCurrentKey(data.DeliverySourceKey(0, p.ID, sk.ID)).SetStatus("ready").SetContent([]byte("encrypted-content")).SaveX(ctx)
	for _, tc := range []struct {
		status    string
		expires   int64
		max, want int64
	}{
		{"ready", 0, 0, -1}, {"paused", 0, 0, 0}, {"ready", time.Now().Add(-time.Minute).Unix(), 0, 0}, {"ready", 0, 3, 3},
	} {
		d.Client.ProductDeliverySource.UpdateOne(source).SetStatus(tc.status).SetExpiresAt(tc.expires).SetMaxDeliveries(tc.max).ExecX(ctx)
		stocks, err := data.ProductStocks(ctx, d, []*ent.Product{p})
		if err != nil || stocks[p.ID] != tc.want {
			t.Fatalf("reuse %s/%d stock: %v %v", tc.status, tc.expires, stocks, err)
		}
	}
}

func TestLegacySupplyCatalogDoesNotExposeRealSKUs(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	plain := d.Client.Product.Create().SetName("default pool").SetSlug("default-pool").SetStatus(1).SaveX(ctx)
	multi := d.Client.Product.Create().SetName("multi pool").SetSlug("multi-pool").SetStatus(1).SaveX(ctx)
	d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(multi.ID).SetName("month").SaveX(ctx)
	d.Client.Product.Create().SetSubsiteID(8).SetName("foreign").SetSlug("foreign").SetStatus(1).SaveX(ctx)
	repo := NewProductRepoImpl(d, nil)
	rows, total, err := repo.ListForSupply(ctx, port.AdminFilter{Status: -1, Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != plain.ID {
		t.Fatalf("legacy catalog advertised unsupported pool: %v %d %v", rows, total, err)
	}
	if _, err = repo.GetForSupply(ctx, multi.ID); err == nil {
		t.Fatal("legacy supply detail accepted a real SKU product")
	}
	// SKU-aware consumers still admit this ordinary automatic product.
	if err = data.RejectServiceProduct(ctx, d.Client, multi); err != nil {
		t.Fatalf("supply guard leaked into shared SKU-aware eligibility: %v", err)
	}
	foreign := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 8})
	rows, total, err = repo.ListForSupply(foreign, port.AdminFilter{Status: -1, Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID == plain.ID {
		t.Fatalf("supply catalog leaked tenant products: %v %d %v", rows, total, err)
	}
}
