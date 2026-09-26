package supply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"testing"
	"time"
)

type monitorAdapter struct {
	adapter.Adapter
	values map[string]int32
	calls  []string
	fail   error
	before func()
}

func (a *monitorAdapter) GetStock(ctx context.Context, code, sku string) (int32, error) {
	a.calls = append(a.calls, code+":"+sku)
	if a.before != nil {
		a.before()
	}
	if a.fail != nil {
		return -2, a.fail
	}
	return a.values[code+":"+sku], nil
}
func monitorFixture(t *testing.T) (*SyncService, *SupplyRepoImpl, *ent.SupplyConnection, *monitorAdapter) {
	t.Helper()
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, r, nil, "monitor")
	conn = r.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetSettings(map[string]any{"schedule": map[string]any{"low_stock": map[string]any{"enabled": true, "threshold": 10, "interval": 5}, "request_delay": 0}}).SaveX(ctx)
	fake := &monitorAdapter{values: map[string]int32{}}
	s.stockAdapterFactory = func(*ent.SupplyConnection) (adapter.Adapter, error) { return fake, nil }
	s.pacer = nil
	_ = ctx
	return s, r, conn, fake
}
func monitorProduct(t *testing.T, r *SupplyRepoImpl, c *ent.SupplyConnection, code string, stock int32) (*ent.Product, *ent.SupplyMapping) {
	t.Helper()
	ctx := context.Background()
	p := r.entClient(ctx).Product.Create().SetName(code).SetSlug(code).SetStatus(1).SetUpstreamSourceID(c.ID).SetUpstreamProductCode(code).SaveX(ctx)
	m := r.entClient(ctx).SupplyMapping.Create().SetConnectionID(c.ID).SetLocalProductID(p.ID).SetUpstreamProduct(code).SetUpStock(stock).SetStockReference(stock).SetStockCheckedAt(time.Now().Add(-10 * time.Minute)).SetStockReferenceAt(time.Now().Add(-10 * time.Minute)).SaveX(ctx)
	return p, m
}
func TestLowStockProbeThresholdAndRecovery(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	_, low := monitorProduct(t, r, c, "low", 10)
	monitorProduct(t, r, c, "high", 11)
	monitorProduct(t, r, c, "infinite", -1)
	a.values["low:"] = 50
	// A long-running import must not block stock-only probes.
	r.entClient(ctx).SupplyConnection.UpdateOneID(c.ID).SetSyncLeaseUntil(time.Now().Add(time.Hour).Unix()).ExecX(ctx)
	s.ScanLowStock(ctx)
	if len(a.calls) != 1 || a.calls[0] != "low:" {
		t.Fatalf("wrong probes %v", a.calls)
	}
	row := r.entClient(ctx).SupplyMapping.GetX(ctx, low.ID)
	if row.UpStock != 50 || row.StockProbeLease != 0 {
		t.Fatal(row)
	}
	r.entClient(ctx).SupplyMapping.UpdateOneID(low.ID).SetStockCheckedAt(time.Now().Add(-10 * time.Minute)).ExecX(ctx)
	s.ScanLowStock(ctx)
	if len(a.calls) != 1 {
		t.Fatal("recovered stock still accelerated")
	}
}
func TestLowStockProbeFailureKeepsReferenceAndBacksOff(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	_, m := monitorProduct(t, r, c, "low", 2)
	a.fail = errors.New("upstream timeout")
	s.ScanLowStock(ctx)
	row := r.entClient(ctx).SupplyMapping.GetX(ctx, m.ID)
	if row.UpStock != -2 || row.StockReference != 2 || row.StockProbeFailures != 1 || row.StockProbeAfter <= time.Now().Unix() {
		t.Fatal(row)
	}
	s.ScanLowStock(ctx)
	if len(a.calls) != 1 {
		t.Fatal("retry storm")
	}
}
func TestLowStockProbeSKUAndLocalIsolation(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	p, parent := monitorProduct(t, r, c, "p", 100)
	for _, code := range []string{"scarce", "plenty", "local"} {
		sk := r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName(code).SetSpecValues(map[string]string{}).SetUpstreamSkuID(code)
		if code == "local" {
			sk.SetFulfillmentMode("local")
		}
		sk.SaveX(ctx)
	}
	a.values["p:scarce"] = 2
	a.values["p:plenty"] = 100
	s.ScanLowStock(ctx)
	if len(a.calls) != 2 {
		t.Fatalf("wrong SKU probes %v", a.calls)
	}
	rows := r.entClient(ctx).SupplyMapping.Query().Where(supplymapping.LocalProductID(p.ID), supplymapping.UpstreamSkuNEQ("")).AllX(ctx)
	if len(rows) != 2 {
		t.Fatal("missing SKU state", len(rows))
	}
	for _, m := range rows {
		if m.UpstreamSku == "scarce" && m.UpStock != 2 {
			t.Fatal(m)
		}
	}
	if r.entClient(ctx).SupplyMapping.GetX(ctx, parent.ID).UpStock != 100 {
		t.Fatal("SKU result overwrote aggregate")
	}
}
func TestLowStockProbeConcurrentLockDiscardsResult(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	p, m := monitorProduct(t, r, c, "p", 2)
	a.values["p:"] = 0
	a.before = func() { r.entClient(ctx).Product.UpdateOneID(p.ID).SetIsLocked(true).AddLockVersion(1).ExecX(ctx) }
	s.ScanLowStock(ctx)
	if r.entClient(ctx).SupplyMapping.GetX(ctx, m.ID).UpStock != 2 {
		t.Fatal("locked product changed")
	}
}
func TestLowStockConfigurationValidation(t *testing.T) {
	for _, raw := range []string{`{"enabled":true,"threshold":0,"interval":5}`, `{"enabled":true,"threshold":10,"interval":1}`, `{"enabled":true,"threshold":1.5,"interval":5}`, `null`} {
		var v any
		_ = json.Unmarshal([]byte(raw), &v)
		if validateLowStockPlan(map[string]any{"schedule": map[string]any{"low_stock": v}}) == nil {
			t.Fatal(raw)
		}
	}
}

func TestLowStockProbeFollowLocalDoesNotStarveUpstream(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	// More than one candidate page of obsolete upstream mappings must not block a live item.
	for i := 0; i < 32; i++ {
		p, _ := monitorProduct(t, r, c, fmt.Sprintf("local-%d", i), 2)
		r.entClient(ctx).Product.UpdateOneID(p.ID).SetFulfillmentMode("local").ExecX(ctx)
		sk := r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName("follow local").SetSpecValues(map[string]string{}).SetUpstreamSkuID("a").SaveX(ctx)
		r.entClient(ctx).SupplyMapping.Create().SetConnectionID(c.ID).SetLocalProductID(p.ID).SetLocalSkuID(sk.ID).SetUpstreamProduct(p.UpstreamProductCode).SetUpstreamSku("a").SetUpStock(2).SaveX(ctx)
	}
	monitorProduct(t, r, c, "live", 2)
	a.values["live:"] = 1
	s.ScanLowStock(ctx)
	if len(a.calls) != 1 || a.calls[0] != "live:" {
		t.Fatalf("invalid mappings blocked probes: %v", a.calls)
	}
}

func TestLowStockProbeAggregatesConfirmedSKUs(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	p, parent := monitorProduct(t, r, c, "sku-total", 100)
	for _, code := range []string{"a", "b"} {
		r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName(code).SetSpecValues(map[string]string{}).SetUpstreamSkuID(code).SaveX(ctx)
	}
	a.values["sku-total:a"] = 2
	a.values["sku-total:b"] = 3
	s.ScanLowStock(ctx)
	if row := r.entClient(ctx).SupplyMapping.GetX(ctx, parent.ID); row.UpStock != 5 {
		t.Fatalf("aggregate not refreshed: %v", row.UpStock)
	}
}

func TestLowStockProbeMissingAggregateKeepsSKUResult(t *testing.T) {
	s, r, c, a := monitorFixture(t)
	ctx := context.Background()
	p, parent := monitorProduct(t, r, c, "sku-only", 100)
	r.entClient(ctx).SupplyMapping.DeleteOneID(parent.ID).ExecX(ctx)
	r.entClient(ctx).ProductSku.Create().SetProductID(p.ID).SetName("a").SetSpecValues(map[string]string{}).SetUpstreamSkuID("a").SaveX(ctx)
	a.values["sku-only:a"] = 2
	s.ScanLowStock(ctx)
	m := r.entClient(ctx).SupplyMapping.Query().Where(supplymapping.LocalProductID(p.ID), supplymapping.UpstreamSku("a")).OnlyX(ctx)
	if m.UpStock != 2 {
		t.Fatalf("missing aggregate discarded SKU observation: %d", m.UpStock)
	}
}
