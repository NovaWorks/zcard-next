package supply

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

type listingStockAdapter struct {
	adapter.Adapter
	values map[string]int32
	fail   string
	calls  int
}

func (a *listingStockAdapter) GetStock(ctx context.Context, code, sku string) (int32, error) {
	a.calls++
	if sku == a.fail {
		return -2, errors.New("timeout")
	}
	n, ok := a.values[sku]
	if !ok {
		return -2, errors.New("unknown")
	}
	return n, nil
}
func TestListingChecksLocalSKUSet(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	c := mustConn(t, r, nil, "listing")
	p := r.entClient(ctx).Product.Create().SetName("p").SetSlug("listing").SetUpstreamSourceID(c.ID).SetUpstreamProductCode("p").SaveX(ctx)
	for _, code := range []string{"a", "b"} {
		r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName(code).SetUpstreamSkuID(code).SetSpecValues(map[string]string{}).SaveX(ctx)
	}
	up := &adapter.Product{ID: "p", IsActive: true, Stock: 999, SKUs: []adapter.SKU{{Code: "a", IsActive: true}, {Code: "b", IsActive: true}, {Code: "not-sold-here", IsActive: true, Stock: 999}}}
	fake := &listingStockAdapter{values: map[string]int32{"a": 0, "b": 0}}
	if e := s.listingSKUStock(ctx, fake, c.ID, up); e != nil || up.Stock != 0 {
		t.Fatal("foreign SKU caused false availability", e, up.Stock)
	}
	fake.fail = "b"
	if e := s.listingSKUStock(ctx, fake, c.ID, up); e != nil || up.Stock != -2 {
		t.Fatal("unknown treated as empty", e, up.Stock)
	}
	up.StockError = ""
	fake.values["a"] = 5
	if e := s.listingSKUStock(ctx, fake, c.ID, up); e != nil || up.Stock != 5 {
		t.Fatal("one confirmed local SKU should sell", e, up.Stock)
	}
}
func TestListingScopeDoesNotUnlistManualProduct(t *testing.T) {
	s, r, _, fm := newTestSyncService(t)
	ctx := context.Background()
	c := mustConn(t, r, nil, "manual")
	p := r.entClient(ctx).Product.Create().SetName("p").SetSlug("manual").SetStatus(1).SetUpstreamSourceID(c.ID).SetUpstreamProductCode("p").SaveX(ctx)
	m := r.entClient(ctx).SupplyMapping.Create().SetConnectionID(c.ID).SetUpstreamProduct("p").SetLocalProductID(p.ID).SaveX(ctx)
	e := data.Tx(ctx, r.data, func(ctx context.Context) error {
		return s.syncStatusOnly(ctx, c, m, &adapter.Product{ID: "p", Stock: 0, IsActive: false, StockCheckedAt: time.Now()}, &TaskProgress{}, true)
	})
	if e != nil || r.entClient(ctx).Product.GetX(ctx, p.ID).Status != 1 || len(fm.statusCalls) != 0 {
		t.Fatal("listing scope changed manually managed product", e)
	}
}

type listingQueue struct{ tasks []queue.Task }

func (q *listingQueue) Enabled() bool { return true }
func (q *listingQueue) Enqueue(_ context.Context, t queue.Task) error {
	q.tasks = append(q.tasks, t)
	return nil
}
func TestListingOptInSchedulesFullCheckWithoutGeneralSchedule(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	c := mustConn(t, r, nil, "scheduled")
	q := &listingQueue{}
	s.enq = q
	p := r.entClient(ctx).Product.Create().SetName("p").SetSlug("scheduled").SetUpstreamSourceID(c.ID).SetUpstreamProductCode("p").SetAutoListing(true).SetIsLocked(true).SaveX(ctx)
	scheduler := NewScheduler(r, s, slog.Default())
	scheduler.Scan(ctx)
	if len(q.tasks) != 0 {
		t.Fatal("locked products activated a schedule")
	}
	r.entClient(ctx).Product.UpdateOneID(p.ID).SetIsLocked(false).ExecX(ctx)
	scheduler.Scan(ctx)
	scheduler.Scan(ctx)
	if len(q.tasks) != 1 {
		t.Fatal("no schedule or duplicate schedule", len(q.tasks))
	}
	task := r.entClient(ctx).SupplySyncTask.Query().OnlyX(ctx)
	if task.Scope != ScopeListing || task.Mode != "full" {
		t.Fatal("inventory change may be omitted by incremental catalog", task.Scope, task.Mode)
	}
}

func TestListingRejectsObservationAfterSKUReplacement(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	c := mustConn(t, r, nil, "changed-sku")
	p := r.entClient(ctx).Product.Create().SetName("p").SetSlug("changed-sku").SetUpstreamSourceID(c.ID).SetUpstreamProductCode("p").SaveX(ctx)
	sk := r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName("a").SetUpstreamSkuID("a").SetSpecValues(map[string]string{}).SaveX(ctx)
	up := &adapter.Product{ID: "p", IsActive: true, Stock: 5, SKUs: []adapter.SKU{{Code: "a", IsActive: true}}}
	fake := &listingStockAdapter{values: map[string]int32{"a": 5}}
	if e := s.listingSKUStock(ctx, fake, c.ID, up); e != nil {
		t.Fatal(e)
	}
	r.entClient(ctx).ProductSku.UpdateOneID(sk.ID).SetUpstreamSkuID("replacement").ExecX(ctx)
	e := data.Tx(ctx, r.data, func(ctx context.Context) error {
		if _, e := data.GuardProductWrite(ctx, r.data, p.ID); e != nil {
			return e
		}
		return s.validateListingSKUs(ctx, p.ID, up)
	})
	if e != nil || up.Stock != -2 {
		t.Fatal("old SKU result accepted for replacement", e, up.Stock)
	}
}

func TestListingChangeInvalidatesPendingImportActivation(t *testing.T) {
	_, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	p := r.entClient(ctx).Product.Create().SetName("pending").SetSlug("pending-activation").SetStatus(0).SaveX(ctx)
	before := productRevision(p)
	data.ManualListing(r.entClient(ctx).Product.UpdateOneID(p.ID), 0).ExecX(ctx)
	if productRevision(r.entClient(ctx).Product.GetX(ctx, p.ID)) == before {
		t.Fatal("explicit keep-off did not fence pending import activation")
	}
}

func TestListingSkipsArchivedProductsWithoutBlockingChannel(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	c := mustConn(t, r, nil, "archived-listing")
	r.entClient(ctx).Product.Create().SetName("removed").SetSlug("removed-listing").SetStatus(-1).SetUpstreamSourceID(c.ID).SetUpstreamProductCode("removed").SaveX(ctx)
	skip, e := s.maintenanceProtected(ctx, c.ID, "removed")
	if e != nil || !skip {
		t.Fatal("archived product did not skip", e)
	}
}

type listingReplacingWriter func(context.Context, catalogport.UpstreamProductInput) (uint64, bool, error)

func (f listingReplacingWriter) UpsertUpstreamProduct(ctx context.Context, in catalogport.UpstreamProductInput) (uint64, bool, error) {
	return f(ctx, in)
}

func TestListingCollectionDoesNotRestoreFromReplacedSKU(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	c := seedConnAndMapping(t, r, false, nil)
	m, e := r.GetMapping(ctx, c.ID, "P1", "")
	if e != nil {
		t.Fatal(e)
	}
	p := r.entClient(ctx).Product.UpdateOneID(m.LocalProductID).SetUpstreamSourceID(c.ID).SetUpstreamProductCode("P1").SetStatus(0).SetAutoListing(true).SetListingReason("stock_out").SaveX(ctx)
	sk := r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName("old").SetUpstreamSkuID("old").SetSpecValues(map[string]string{}).SaveX(ctx)
	up := &adapter.Product{ID: "P1", Name: "p", Price: 1000, IsActive: true, Stock: 5, StockCheckedAt: time.Now(), SKUs: []adapter.SKU{{Code: "old", IsActive: true}}}
	if e := s.listingSKUStock(ctx, &listingStockAdapter{values: map[string]int32{"old": 5}}, c.ID, up); e != nil {
		t.Fatal(e)
	}
	s.writer = listingReplacingWriter(func(ctx context.Context, _ catalogport.UpstreamProductInput) (uint64, bool, error) {
		err := r.entClient(ctx).ProductSku.UpdateOneID(sk.ID).SetUpstreamSkuID("new").Exec(ctx)
		return p.ID, false, err
	})
	if _, e := s.syncOne(ctx, 0, collectTask(), c, up, nil, &TaskProgress{}); e != nil {
		t.Fatal(e)
	}
	current := r.entClient(ctx).Product.GetX(ctx, p.ID)
	m, e = r.GetMapping(ctx, c.ID, "P1", "")
	if e != nil || current.Status != 0 || current.ListingLastStock != -2 || m.UpStock != -2 {
		t.Fatal("collection reused stock from replaced SKU", e, current.Status, current.ListingLastStock, m.UpStock)
	}
}
