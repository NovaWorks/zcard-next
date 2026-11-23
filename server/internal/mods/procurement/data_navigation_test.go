package procurement

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
	"testing"
)

func TestACGPurchaseRetainsOriginalPageID(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same binding", true: "different purchase code"}[changed], func(t *testing.T) {
			repo, d := newProcureTestData(t)
			ctx := context.Background()
			_, iid := seedOrderItem(t, d)
			conn := d.Client.SupplyConnection.Create().SetName("direct").SetDriver("acg_faka").SetBaseURL("https://supplier.example").SetCredentials([]byte("test")).SaveX(ctx)
			source, e := data.CaptureUpstreamSource(ctx, d.Client, conn.ID, "CA814E42E563538A", "sku", "order", "1111")
			if e != nil {
				t.Fatal(e)
			}
			d.Client.OrderItem.UpdateOneID(iid).SetUpstreamSourceSnapshot(source).ExecX(ctx)
			it := d.Client.OrderItem.GetX(ctx, iid)
			d.Client.Product.Create().SetID(it.ProductID).SetName("rebound").SetSlug("rebound").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("new").SetUpstreamNavigation(upstreamsource.Navigation(conn.ID, "new", "9999")).SaveX(ctx)
			code := "CA814E42E563538A"
			want := "https://supplier.example/item/1111"
			if changed {
				code = "another-code"
				want = ""
			}
			po, e := repo.CreatePending(ctx, iid, conn.ID, code, 1, "manual", "", "sku")
			if e != nil {
				t.Fatal(e)
			}
			if po.UpstreamSourceSnapshot["product_url"] != want || po.UpstreamSourceSnapshot["product_code"] != code {
				t.Fatal(po.UpstreamSourceSnapshot)
			}
			d.Client.SupplyConnection.DeleteOneID(conn.ID).ExecX(ctx)
			rows, e := data.ProcurementSources(ctx, d.Client, []*ent.ProcurementOrder{po})
			if e != nil || rows[po.ID]["product_url"] != want {
				t.Fatal(rows, e)
			}
		})
	}
}
