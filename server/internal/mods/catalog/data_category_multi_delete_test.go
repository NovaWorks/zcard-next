package catalog

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"testing"
)

func TestCategoryDeleteCascadesAndPreservesOutstandingHistory(t *testing.T) {
	d, s, p, o, it := deleteFixture(t, order.StatusFulfilling)
	ctx := context.Background()
	parent := d.Client.Category.Create().SetName("parent").SaveX(ctx)
	child := d.Client.Category.Create().SetName("child").SetParentID(parent.ID).SaveX(ctx)
	grandchild := d.Client.Category.Create().SetName("grandchild").SetParentID(child.ID).SaveX(ctx)
	keep := d.Client.Category.Create().SetName("unrelated").SaveX(ctx)
	foreign := d.Client.Category.Create().SetName("foreign").SetParentID(parent.ID).SetSubsiteID(9).SaveX(ctx)
	d.Client.Product.UpdateOneID(p.ID).SetCategoryID(grandchild.ID).SetIsLocked(true).SetStatus(2).ExecX(ctx)
	reserve := d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("reserved")).SetContentHash("reserved").SetStatus(card.StatusReserved).SetOrderID(o.ID).SaveX(ctx)
	available := d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("available")).SetContentHash("available").SetStatus(card.StatusAvailable).SaveX(ctx)
	payment := d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("wallet").SetAmount(100).SetStatus("success").SaveX(ctx)
	refund := d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(10).SetChannel("wallet").SetStatus("processing").SaveX(ctx)
	proc := d.Client.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(1).SetDedupeKey("pending").SetStatus("pending").SaveX(ctx)
	untouched := d.Client.Product.Create().SetName("other").SetSlug("other").SetCategoryID(keep.ID).SetPrice(1).SaveX(ctx)
	alien := d.Client.Product.Create().SetName("foreign").SetSlug("foreign").SetCategoryID(foreign.ID).SetSubsiteID(9).SetPrice(1).SaveX(ctx)
	if _, e := s.DeleteCategory(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9}), &adminv1.DeleteCategoryRequest{Id: parent.ID}); e == nil {
		t.Fatal("cross tenant cascade allowed")
	}
	if _, e := s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: parent.ID}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []uint64{parent.ID, child.ID, grandchild.ID} {
		if _, e := d.Client.Category.Get(ctx, id); !ent.IsNotFound(e) {
			t.Fatal("category remains", id, e)
		}
	}
	if got := d.Client.Product.GetX(ctx, p.ID); got.Status != -1 || got.CategoryID != 0 {
		t.Fatal("live product remains")
	}
	if d.Client.OrderItem.GetX(ctx, it.ID).ProductName != p.Name || d.Client.Order.GetX(ctx, o.ID).Status != order.StatusFulfilling {
		t.Fatal("order snapshot/status damaged")
	}
	if d.Client.Card.GetX(ctx, reserve.ID).Status != card.StatusReserved || d.Client.Card.GetX(ctx, available.ID).Status != card.StatusAvailable {
		t.Fatal("outstanding fulfillment stock damaged")
	}
	if d.Client.Payment.GetX(ctx, payment.ID).OrderID != o.ID || d.Client.RefundOrder.GetX(ctx, refund.ID).Status != "processing" || d.Client.ProcurementOrder.GetX(ctx, proc.ID).Status != "pending" {
		t.Fatal("financial/fulfillment history damaged")
	}
	if d.Client.Product.GetX(ctx, untouched.ID).Status != 1 || d.Client.Product.GetX(ctx, alien.ID).Status != 1 || d.Client.Category.Query().CountX(ctx) != 2 {
		t.Fatal("unrelated or foreign catalog damaged")
	}
}

func TestCategoryCascadeChunksAndRollback(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	parent := d.Client.Category.Create().SetName("root").SaveX(ctx)
	for i := 0; i < 205; i++ {
		cat := d.Client.Category.Create().SetName(fmt.Sprint(i)).SetParentID(parent.ID).SaveX(ctx)
		d.Client.Product.Create().SetName("child product").SetSlug(fmt.Sprint(i)).SetCategoryID(cat.ID).SetPrice(1).SaveX(ctx)
	}
	fail := true
	archived := 0
	d.Client.Product.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if pm, ok := m.(*ent.ProductMutation); ok {
				if status, ok := pm.Status(); fail && ok && status == -1 {
					archived++
					if archived == 3 {
						return nil, fmt.Errorf("injected archive failure")
					}
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	if _, e := s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: parent.ID}); e == nil {
		t.Fatal("failure not propagated")
	}
	if d.Client.Category.Query().CountX(ctx) != 206 || d.Client.Product.Query().CountX(ctx) != 205 {
		t.Fatal("failed cascade partially committed")
	}
	fail = false
	if _, e := s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: parent.ID}); e != nil {
		t.Fatal(e)
	}
	if d.Client.Category.Query().CountX(ctx) != 0 {
		t.Fatal("chunked category deletion incomplete")
	}
	for _, p := range d.Client.Product.Query().AllX(ctx) {
		if p.Status != -1 || p.CategoryID != 0 {
			t.Fatal("chunked product deletion incomplete")
		}
	}
}
