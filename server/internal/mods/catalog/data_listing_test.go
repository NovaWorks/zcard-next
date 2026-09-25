package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestListingManualLockUnknownAndHiddenRecovery(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	p := d.Client.Product.Create().SetName("hidden").SetSlug("listing-hidden").SetStatus(2).SetPrice(100).SetUpstreamSourceID(1).SetAutoListing(true).SaveX(ctx)
	base := time.Now().Add(-3 * time.Minute)
	observe := func(n int32, active bool, at time.Time) {
		t.Helper()
		err := data.Tx(ctx, d, func(ctx context.Context) error {
			p, e := data.GuardProductWrite(ctx, d, p.ID)
			if e != nil {
				return e
			}
			return data.ObserveListing(ctx, d, p, n, active, at)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	observe(0, true, base)
	if d.Client.Product.GetX(ctx, p.ID).Status != 2 {
		t.Fatal("single zero removed product")
	}
	observe(-2, true, base.Add(time.Minute))
	observe(0, true, base.Add(90*time.Second))
	if d.Client.Product.GetX(ctx, p.ID).Status != 2 {
		t.Fatal("failed query counted as confirmation")
	}
	observe(0, true, base.Add(130*time.Second))
	off := d.Client.Product.GetX(ctx, p.ID)
	if off.Status != 0 || off.ListingReason != "stock_out" || off.ListingRestoreStatus != 2 {
		t.Fatalf("wrong auto off: %+v", off)
	}
	observe(-1, true, base.Add(140*time.Second))
	if d.Client.Product.GetX(ctx, p.ID).Status != 2 {
		t.Fatal("hidden listing became public")
	}
	// Explicit manual off invalidates an older in-flight observation and pauses automation.
	if _, _, e := s.repo.batchStatus(ctx, []uint64{p.ID}, 0); e != nil {
		t.Fatal(e)
	}
	observe(5, true, base.Add(150*time.Second))
	manual := d.Client.Product.GetX(ctx, p.ID)
	if manual.Status != 0 || manual.AutoListing || manual.ListingReason != "manual" {
		t.Fatal("manual intent overwritten")
	}
	// A locked product cannot be observed by the maintenance writer.
	_, e := s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, IsLocked: true})
	if e != nil {
		t.Fatal(e)
	}
	e = data.Tx(ctx, d, func(ctx context.Context) error { _, e := data.GuardProductWrite(ctx, d, p.ID); return e })
	if !data.IsProductLocked(e) {
		t.Fatal("lock not enforced")
	}
}

func TestListingUnavailableAndLocalSourcesNeverRestore(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	for _, mode := range []string{"auto", "reuse", "local"} {
		p := d.Client.Product.Create().SetName(mode).SetSlug(mode).SetStatus(0).SetPrice(100).SetUpstreamSourceID(1).SetAutoListing(true).SetListingReason("stock_out").SetFulfillmentMode(mode).SaveX(ctx)
		for i, active := range []bool{false, true} {
			e := data.Tx(ctx, d, func(ctx context.Context) error {
				current, e := data.GuardProductWrite(ctx, d, p.ID)
				if e != nil {
					return e
				}
				return data.ObserveListing(ctx, d, current, 5, active, at.Add(time.Duration(i)*time.Second))
			})
			if e != nil {
				t.Fatal(e)
			}
		}
		if d.Client.Product.GetX(ctx, p.ID).Status != 0 {
			t.Fatalf("%s restored unsafely", mode)
		}
	}
}

func TestListingCombinedInventoryAndRestockFilters(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	for i, status := range []int8{0, 1, 2} {
		p := d.Client.Product.Create().SetName("empty").SetSlug(fmt.Sprint(i)).SetStatus(status).SaveX(ctx)
		rows, total, e := s.repo.ListAdmin(ctx, port.AdminFilter{Status: map[int8]int8{0: -1, 1: 1, 2: 2}[status], Inventory: "empty", Page: 1, PageSize: 1})
		if e != nil || total != 1 || len(rows) != 1 || rows[0].ID != p.ID {
			t.Fatalf("status %d inventory filter: %d %v", status, total, e)
		}
	}
	p := d.Client.Product.Create().SetName("restocked").SetSlug("restocked").SetStatus(0).SetListingRestocked(true).SetUpstreamSourceID(1).SetUpstreamProductCode("r").SaveX(ctx)
	m := d.Client.SupplyMapping.Create().SetConnectionID(1).SetUpstreamProduct("r").SetUpstreamSku("").SetLocalProductID(p.ID).SetUpStock(8).SetStockCheckedAt(time.Now()).SaveX(ctx)
	rows, total, e := s.repo.ListAdmin(ctx, port.AdminFilter{RestockedOnly: true, Inventory: "available"})
	if e != nil || total != 1 || rows[0].ID != p.ID {
		t.Fatal("restock filter", e, total)
	}
	d.Client.SupplyMapping.UpdateOneID(m.ID).SetStockCheckedAt(time.Now().Add(-10 * time.Minute)).ExecX(ctx)
	_, total, e = s.repo.ListAdmin(ctx, port.AdminFilter{RestockedOnly: true})
	if e != nil || total != 0 {
		t.Fatal("stale inventory advertised as restock")
	}
	_, total, e = s.repo.ListAdmin(ctx, port.AdminFilter{Status: -1, Inventory: "unknown"})
	if e != nil || total != 1 {
		t.Fatal("stale missing from unknown")
	}
}

func TestListingBatchPreviewConflictScopeAndRetry(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	conn := d.Client.SupplyConnection.Create().SetName("up").SetDriver("zcard").SetBaseURL("https://example.com").SetCredentials([]byte("test")).SaveX(ctx)
	makeP := func(name string) *ent.Product {
		return d.Client.Product.Create().SetName(name).SetSlug(name).SetStatus(0).SetPrice(100).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode(name).SaveX(ctx)
	}
	a, b, c := makeP("a"), makeP("b"), makeP("c")
	d.Client.Product.UpdateOneID(c.ID).SetIsLocked(true).ExecX(ctx)
	preview, e := s.ManageProductListing(ctx, &adminv1.ManageProductListingRequest{Action: "enable", Filter: &adminv1.ListProductsRequest{Status: -1}})
	if e != nil || preview.Matched != 3 || preview.SkippedLocked != 1 {
		t.Fatal(preview, e)
	}
	revisions := map[uint64]string{}
	for _, p := range preview.Items {
		if p.Result == "ready" {
			revisions[p.Id] = p.Revision
		}
	}
	d.Client.Product.UpdateOneID(b.ID).SetName("changed").ExecX(ctx)
	req := &adminv1.ManageProductListingRequest{Action: "enable", Apply: true, Revisions: revisions}
	result, e := s.ManageProductListing(ctx, req)
	if e != nil || result.Changed != 1 || result.Conflicts != 1 {
		t.Fatal(result, e)
	}
	if got := d.Client.Product.GetX(ctx, a.ID); !got.AutoListing || got.Status != 0 || got.ListingReason != "stock_out" {
		t.Fatal("enabling did not wait for fresh stock")
	}
	result, e = s.ManageProductListing(ctx, req)
	if e != nil || result.Changed != 0 {
		t.Fatal("replayed batch overwrote new state")
	}
	foreign := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99})
	result, e = s.ManageProductListing(foreign, req)
	if e != nil || result.Changed != 0 {
		t.Fatal("cross tenant write", e)
	}
	// A local SKU makes the whole imported product ineligible for automatic management.
	d.Client.ProductSku.Create().SetProductID(b.ID).SetName("local").SetSpecValues(map[string]string{}).SetFulfillmentMode("local").SaveX(ctx)
	preview, e = s.ManageProductListing(ctx, &adminv1.ManageProductListingRequest{Action: "enable", Ids: []uint64{b.ID}})
	if e != nil || preview.Unsupported != 1 {
		t.Fatal("mixed source accepted", e)
	}
}
