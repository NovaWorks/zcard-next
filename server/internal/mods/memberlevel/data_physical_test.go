package memberlevel

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"testing"
)

func TestPhysicalPointsRefundDebtAndSpending(t *testing.T) {
	ctx := context.Background()
	d, repo, s, w := newMemberLevelEnv(t)
	s.points = wallet.ProvidePortPoints(w)
	if _, e := repo.CreateLevel(ctx, "base", "consume", 0, 0, 0, 1, true, map[string]any{"spend_cents": 100, "points": 1}); e != nil {
		t.Fatal(e)
	}
	o := d.Client.Order.Create().SetOrderNo("PHYS-POINTS").SetUserID(7).SetStatus("paid").SetCommerceVersion(1).SetTotalAmount(1300).SetShippingAmount(300).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetPaidAmount(1000).SetFulfillmentType("shipping").SaveX(ctx)
	paid := events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"user_id":7,"total_cents":1000}`, o.ID))}
	if e := s.OnOrderPaid(ctx, paid); e != nil {
		t.Fatal(e)
	}
	if e := w.PointDebitInTx(ctx, wallet.PointEntry{UserID: 7, Direction: "out", Type: "redeem", Amount: 8, Reference: "spent"}); e != nil {
		t.Fatal(e)
	}
	d.Client.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(500).ExecX(ctx)
	refund := events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"commerce_version":1}`, o.ID))}
	if e := s.OnOrderRefunded(ctx, refund); e != nil {
		t.Fatal(e)
	}
	if e := s.OnOrderRefunded(ctx, refund); e != nil {
		t.Fatal(e)
	}
	if points, _ := w.GetPoints(ctx, 7); points != -3 {
		t.Fatalf("spent points must become refund debt: %d", points)
	}
	totals, e := data.UserSpending(ctx, d.Client, []uint64{7})
	if e != nil || totals[7] != 500 {
		t.Fatalf("spending includes shipping or refunded goods: %v %v", totals, e)
	}
}
