//go:build integration

package testint

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"sync"
	"testing"
)

func TestPhysicalRefundRetryMySQL(t *testing.T) { physicalRefundRetry(MySQL(t)) }
func TestPhysicalRefundRetryPG(t *testing.T)    { physicalRefundRetry(PG(t)) }
func physicalRefundRetry(h *Harness) {
	t := h.T
	d := h.Data
	ctx := context.Background()
	r := payment.NewPaymentRepoImpl(d, nil, nil, nil, nil, nil, data.NewOutboxWriter(d), nil, nil, nil)
	p := d.Client.Product.Create().SetName("Retry").SetSlug("retry").SetGoodsType("physical").SetPrice(500).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("RETRY-ORDER").SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(2000).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(4).SetUnitPrice(500).SetAmount(2000).SetPaidAmount(2000).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	zero := int64(0)
	req := &adminv1.CreateRefundRequest{Channel: "gateway", RequestKey: "same-cancel-key", ExpectedRefundedCents: &zero, ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"cancel_quantity":1}]`, it.ID)}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, _ = r.RefundPhysical(ctx, o.ID, 7, req) }()
	}
	close(start)
	wg.Wait()
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e != nil {
		t.Fatal("retry failed", e)
	}
	if d.Client.OrderItem.GetX(ctx, it.ID).CanceledQuantity != 1 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 || d.Client.RefundOrder.Query().CountX(ctx) != 1 {
		t.Fatal("concurrent retry duplicated cancellation")
	}
	req.ItemAllocationsJson = fmt.Sprintf(`[{"item_id":%d,"cancel_quantity":2}]`, it.ID)
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e == nil {
		t.Fatal("changed retry accepted")
	}
}
