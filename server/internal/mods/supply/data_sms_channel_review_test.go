package supply

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"testing"
	"time"
)

func TestChannelSkipsAggregateInventoryAcrossMaintenancePaths(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := newTestSyncService(t)
	c := mustConn(t, r, nil, "channel-review")
	c = r.entClient(ctx).SupplyConnection.UpdateOneID(c.ID).SetDriver("zcard").SetExchangeRate(1).SaveX(ctx)
	writer := catalog.NewProductRepoImpl(r.data, nil)
	s.writer, s.maintainer = writer, writer
	p := &adapter.Product{ID: "channel", Name: "service", ProductKind: "sms_channel", DeliveryKind: "sms_activation", IsActive: true, Stock: -2}
	if _, e := s.ImportOne(ctx, c, p, nil, PriceModeFixed, 0, 200); e != nil {
		t.Fatal(e)
	}
	a := &limitedStockUpstream{fakeUpstream: &fakeUpstream{products: []adapter.Product{*p}, total: 1, echo: true}}
	if n, e := NewGateway(r, nil, nil).DisplayStock(ctx, c.ID, p.ID); e != nil || n != -2 {
		t.Fatal("channel attempted a live aggregate stock request", e, n)
	}
	for _, scope := range []string{ScopeCollect, ScopeStatus, ScopeListing} {
		task, e := r.CreateSyncTask(ctx, c.ID, "full", scope, false)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.runLoop(ctx, task.ID, task, c, a, loadScheduleSettings(c), scope, false, listOf(a.fakeUpstream)); e != nil {
			t.Fatal(e)
		}
		got, e := r.GetSyncTask(ctx, task.ID)
		if e != nil || got.Status != "done" {
			t.Fatalf("%s falsely reported missing channel stock: %+v %v", scope, got, e)
		}
	}
	task, e := r.CreateSyncTask(ctx, c.ID, "failed", ScopeStock, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.runStockOnly(ctx, task, c, a, loadScheduleSettings(c)); e != nil {
		t.Fatal(e)
	}
	got, e := r.GetSyncTask(ctx, task.ID)
	if e != nil || got.Status != "done" || got.ProcessedCount != 0 {
		t.Fatal("channel included in stock-only recovery", e, got)
	}
	if a.calls.Load() != 0 {
		t.Fatal("channel consumed provider aggregate-stock requests", a.calls.Load())
	}
	local := r.entClient(ctx).Product.Query().OnlyX(ctx)
	if local.Status != 1 || local.Price != 0 {
		t.Fatal("stock maintenance changed the channel product", local.Status, local.Price)
	}
}

func TestChannelExcludedFromLowStockProbe(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	for i := 0; i < 35; i++ {
		code := time.Unix(int64(i), 0).Format("150405")
		p, _ := monitorProduct(t, r, c, code, -2)
		r.entClient(ctx).Product.UpdateOneID(p.ID).SetProductKind("sms_channel").ExecX(ctx)
	}
	monitorProduct(t, r, c, "ordinary", 2)
	a.values["ordinary:"] = 50
	s.ScanLowStock(ctx)
	if len(a.calls) != 1 || a.calls[0] != "ordinary:" {
		t.Fatalf("channel stock starved ordinary product probes: %v", a.calls)
	}
}
