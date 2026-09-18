package supply

import (
	"context"
	"errors"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"testing"
	"time"
)

func TestAuditFailedStockQueryPreservesReference(t *testing.T) {
	repo, d := newTestRepo(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("audit").SetSlug("audit").SetUpstreamSourceID(9).SetUpstreamProductCode("A").SaveX(ctx)
	d.Client.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetUpstreamProduct("A").SetUpStock(37).SetStockCheckedAt(time.Now().Add(-time.Hour)).SaveX(ctx)
	g := NewGateway(repo, nil, nil)
	g.cacheStock(ctx, 9, "A", 0, errors.New("simulated upstream timeout"))
	got, err := data.ProductStockSnapshots(ctx, d, []*ent.Product{p})
	if err != nil {
		t.Fatal(err)
	}
	s := got[p.ID]
	t.Logf("after one timeout: quantity=%d status=%s available=%d", s.Quantity, s.Status, s.Available())
	if s.Quantity != 37 || s.Status != "stale" || s.Available() != -2 {
		t.Fatalf("lost prior successful stock reference: %+v", s)
	}
}

func TestStockReferenceFailureAndOutOfOrderUpdates(t *testing.T) {
	repo, d := newTestRepo(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("ordering").SetSlug("ordering").SetUpstreamSourceID(9).SetUpstreamProductCode("B").SaveX(ctx)
	d.Client.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetUpstreamProduct("B").SetUpStock(-2).SaveX(ctx)
	base := time.Now().Add(-time.Second).Truncate(time.Millisecond)
	for _, step := range []struct {
		offset    time.Duration
		n         int32
		status    string
		reference int64
	}{
		{0, 37, "current", 37},
		{0, 37, "current", 37},
		{100 * time.Millisecond, -2, "stale", 37},
		{200 * time.Millisecond, 42, "current", 42},
		{150 * time.Millisecond, -2, "current", 42},
		{300 * time.Millisecond, 0, "current", 0},
		{400 * time.Millisecond, -2, "stale", 0},
	} {
		if err := repo.recordStock(ctx, 9, "B", "", step.n, base.Add(step.offset)); err != nil {
			t.Fatal(err)
		}
		stocks, err := data.ProductStockSnapshots(ctx, d, []*ent.Product{p})
		if err != nil {
			t.Fatal(err)
		}
		got := stocks[p.ID]
		if got.Status != step.status || got.Quantity != step.reference {
			t.Fatalf("step %+v: %+v", step, got)
		}
		if step.status == "stale" && got.Available() != -2 {
			t.Fatal("reference became purchasable")
		}
	}
	old, err := repo.GetMapping(ctx, 9, "B", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.recordStock(ctx, 9, "B", "", 53, base.Add(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	old.PricingOverride = map[string]any{"last_synced_price": 100}
	if err := repo.UpsertMapping(ctx, old); err != nil {
		t.Fatal(err)
	}
	fresh, _ := repo.GetMapping(ctx, 9, "B", "")
	if fresh.UpStock != 53 {
		t.Fatal("metadata upsert restored stale stock", fresh.UpStock)
	}
}
