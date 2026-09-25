package supply

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"testing"
)

func TestLocalSourceSkipsUpstreamStockAndMaintenance(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	c := s.repo.entClient(ctx)
	p := c.Product.Create().SetName("shared").SetSlug("local-policy").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("P1").SetFulfillmentMode("reuse").SetPrice(123).SaveX(ctx)
	src := c.ProductDeliverySource.Create().SetProductID(p.ID).SetCurrentKey(data.DeliverySourceKey(0, p.ID, 0)).SetStatus("ready").SetContent([]byte("encrypted-fixture")).SaveX(ctx)
	// Missing upstream code in the reader would reject an upstream stock check.
	gw := &Gateway{repo: s.repo, reader: fakeGateReader{prods: map[uint64]*catalogport.Product{p.ID: {ID: p.ID, UpstreamSourceID: conn.ID}}}}
	if e := gw.CheckItems(ctx, 0, []orderport.UpstreamStockItem{{ProductID: p.ID, Quantity: 1}}); e != nil {
		t.Fatal("ready source consulted upstream", e)
	}
	c.ProductDeliverySource.UpdateOneID(src.ID).SetStatus("paused").ExecX(ctx)
	if e := gw.CheckItems(ctx, 0, []orderport.UpstreamStockItem{{ProductID: p.ID, Quantity: 1}}); e == nil {
		t.Fatal("paused source admitted")
	}
	task, e := s.repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	if e != nil {
		t.Fatal(e)
	}
	stats := TaskProgress{}
	_, e = s.sync.syncOne(ctx, task.ID, task, conn, &adapter.Product{ID: "P1", Name: "overwrite", IsActive: true}, nil, &stats)
	if e != nil || stats.ManualSkipped != 1 || c.Product.GetX(ctx, p.ID).Price != 123 {
		t.Fatalf("maintenance failed to skip: %+v %v", stats, e)
	}
}
