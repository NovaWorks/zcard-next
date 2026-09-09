package procurement

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

func TestProcurementDetailSnapshots(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	d.Client.Order.UpdateOneID(oid).SetTotalAmount(901).ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(iid).SetCost(200).SetAmount(1000).SetSkuName("一年版").ExecX(ctx)
	d.Client.OrderItem.Create().SetOrderID(oid).SetProductID(11).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetFulfillmentType("auto").SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetName("采购来源").SetDriver("zcard").SetBaseURL("https://user:secret@up.example/path?token=secret#secret").SetCredentials([]byte("encrypted-secret")).SaveX(ctx)
	po, err := repo.CreatePending(ctx, iid, conn.ID, "SKU-A", 2, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAdminProcurementService(repo, nil)
	got, err := svc.GetProcurement(ctx, &adminv1.GetProcurementRequest{Id: po.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.OrderNo != "T-ORDER-1" || got.SkuName != "一年版" || got.CostTotalCents != 400 || got.AllocatedSaleCents != 450 || got.ProfitCents != 50 || got.ConnectionUrl != "https://up.example/path" || got.UpstreamProductCode != "SKU-A" {
		t.Fatalf("wrong detail: %v", got)
	}
	if _, err = svc.GetProcurement(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99}), &adminv1.GetProcurementRequest{Id: po.ID}); errors.Code(err) != 404 {
		t.Fatalf("tenant access: %v", err)
	}
	d.Client.OrderItem.UpdateOneID(iid).SetCost(0).ExecX(ctx)
	got, err = svc.GetProcurement(ctx, &adminv1.GetProcurementRequest{Id: po.ID})
	if err != nil || got.CostBasis != "unrecorded" || got.ProfitCents != 0 {
		t.Fatalf("unknown cost inflated profit: %v %v", got, err)
	}
}
func TestAllocatedSaleConservesTotal(t *testing.T) {
	items := []*ent.OrderItem{{ID: 1, Amount: 100}, {ID: 2, Amount: 100}, {ID: 3, Amount: 100}}
	var sum int64
	for _, it := range items {
		sum += allocatedSale(101, items, it.ID)
	}
	if sum != 101 {
		t.Fatalf("allocation lost cents: %d", sum)
	}
	items = []*ent.OrderItem{{ID: 1, Amount: 4000000000}, {ID: 2, Amount: 6000000000}}
	if got := allocatedSale(9000000000, items, 1); got != 3600000000 {
		t.Fatalf("large amount overflow: %d", got)
	}
}
