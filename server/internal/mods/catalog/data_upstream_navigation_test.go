package catalog

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
	"testing"
)

func TestUpstreamNavigationSyncAndBinding(t *testing.T) {
	d, r := newMaintainEnv(t)
	ctx := context.Background()
	conn := d.Client.SupplyConnection.Create().SetName("direct").SetDriver("acg_faka").SetBaseURL("https://supplier.example/shop").SetCredentials([]byte("test")).SaveX(ctx)
	in := port.UpstreamProductInput{ConnectionID: conn.ID, UpstreamProductCode: "CA814E42E563538A", UpstreamPageID: "1111", Name: "test", Price: 100, FactoryPrice: 50, Status: 1}
	id, _, err := r.UpsertUpstreamProduct(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	first := d.Client.Product.GetX(ctx, id)
	assertSource := func(want string) {
		t.Helper()
		sources, e := data.CurrentProductSources(ctx, d.Client, []uint64{id})
		if e != nil || sources[id]["product_url"] != want {
			t.Fatal(sources, e)
		}
	}
	assertSource("https://supplier.example/shop/item/1111")
	in.UpstreamPageID = "2222"
	if _, _, err = r.UpsertUpstreamProduct(ctx, in); err != nil {
		t.Fatal(err)
	}
	next := d.Client.Product.GetX(ctx, id)
	if next.LockVersion != first.LockVersion || next.Price != first.Price || next.UpstreamProductCode != first.UpstreamProductCode {
		t.Fatal("navigation changed purchase settings")
	}
	assertSource("https://supplier.example/shop/item/2222")
	// A changed binding cannot accidentally reuse the previous supplier's page ID.
	if upstreamsource.PageID(next.UpstreamNavigation, conn.ID, "another-code") != "" || upstreamsource.PageID(next.UpstreamNavigation, conn.ID+1, in.UpstreamProductCode) != "" {
		t.Fatal("navigation escaped its binding")
	}
	in.UpstreamPageID = ""
	if _, _, err = r.UpsertUpstreamProduct(ctx, in); err != nil {
		t.Fatal(err)
	}
	assertSource("")
}
