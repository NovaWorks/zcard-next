package procurement

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
)

func TestMissingProcurementRetainsOrderSource(t *testing.T) {
	for _, mode := range []string{"rebound", "deleted", "no_history", "legacy_placeholder", "legacy_rebound_placeholder"} {
		t.Run(mode, func(t *testing.T) {
			repo, d := newProcureTestData(t)
			ctx := context.Background()
			_, iid := seedOrderItem(t, d)
			it := d.Client.OrderItem.GetX(ctx, iid)
			original := d.Client.SupplyConnection.Create().SetName("原来源").SetDriver("mcy_shop").SetBaseURL("https://original.example").SetCredentials([]byte("fixture")).SaveX(ctx)
			source, e := data.CaptureUpstreamSource(ctx, d.Client, original.ID, "12", "52", "order")
			if e != nil {
				t.Fatal(e)
			}
			if mode != "no_history" {
				d.Client.OrderItem.UpdateOneID(iid).SetUpstreamSourceSnapshot(source).ExecX(ctx)
			}
			other := d.Client.SupplyConnection.Create().SetName("新来源").SetDriver("mcy_shop").SetBaseURL("https://changed.example").SetCredentials([]byte("fixture")).SaveX(ctx)
			if mode != "deleted" {
				d.Client.Product.Create().SetID(it.ProductID).SetName("当前商品").SetSlug("rebound").SetUpstreamSourceID(other.ID).SetUpstreamProductCode("99").SaveX(ctx)
			}
			d.Client.SupplyConnection.DeleteOneID(original.ID).ExecX(ctx)
			if mode == "legacy_placeholder" || mode == "legacy_rebound_placeholder" {
				connection, code := uint64(0), ""
				if mode == "legacy_rebound_placeholder" {
					connection, code = other.ID, "99"
				}
				po, e := repo.CreatePending(ctx, iid, connection, code, 1, "manual", "")
				if e != nil {
					t.Fatal(e)
				}
				if e = repo.MarkManual(ctx, po.ID, "未能生成有效采购任务，请核实上游扣款和出货结果后人工补发；系统不会重复采购"); e != nil {
					t.Fatal(e)
				}
			} else if e = repo.recordMissing(ctx, iid); e != nil {
				t.Fatal(e)
			}
			po, e := repo.GetByOrderItem(ctx, iid)
			if e != nil {
				t.Fatal(e)
			}
			got, e := NewAdminProcurementService(repo, nil).GetProcurement(ctx, &adminv1.GetProcurementRequest{Id: po.ID})
			if e != nil {
				t.Fatal(e)
			}
			f := got.GetUpstreamSource().GetFields()
			if mode == "no_history" {
				if f["kind"].GetStringValue() != "unknown" || f["product_url"].GetStringValue() != "" {
					t.Fatal("invented source", got)
				}
			} else if f["stage"].GetStringValue() != "order" || f["product_url"].GetStringValue() != "https://original.example/item?id=12" || f["sku_id"].GetStringValue() != "52" {
				t.Fatal("missing purchase retargeted", got)
			}
			if e = repo.recordMissing(ctx, iid); e != nil {
				t.Fatal(e)
			}
			if d.Client.ProcurementOrder.Query().CountX(ctx) != 1 {
				t.Fatal("created duplicate purchase")
			}
		})
	}
}
