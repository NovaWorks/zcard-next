package procurement

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

func TestSubmitForwardsImmutableFormAnswers(t *testing.T) {
	s, d, payload, iid := submitFixture(t)
	ctx := context.Background()
	d.Client.OrderItem.UpdateOneID(iid).SetFormAnswers([]map[string]string{{"upstream_key": "handle", "upstream_value": `"@alice"`, "upstream_connection": "1", "upstream_product": "P1"}}).ExecX(ctx)
	g := &submitGateway{}
	g.submit = func(_ context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
		if req.ManualFormData["handle"] != "@alice" {
			t.Fatal("order snapshot was not forwarded")
		}
		return &supplyport.PurchaseResult{UpstreamOrderID: "UP-1", Status: "paid"}, nil
	}
	s.gw = g
	if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
		t.Fatal(err)
	}
}

func TestRefundedSupplierResultNeverDeliversOldCards(t *testing.T) {
	for _, status := range []string{"refunded", "partially_refunded", "refunding", "canceled"} {
		t.Run(status, func(t *testing.T) {
			s, d, payload, iid := submitFixture(t)
			ctx := context.Background()
			po, err := s.repo.claimPurchase(ctx, iid, 1, "P1", 2, "auto_refund", payload.OrderNo)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.confirmResult(ctx, po.ID, status, []string{"old-card"}, 100); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.repo.Get(ctx, po.ID); got.Status != "manual" {
				t.Fatalf("supplier refund needs reconciliation: %s", got.Status)
			}
			if data.Client(ctx, d).OrderDelivery.Query().CountX(ctx) != 0 {
				t.Fatal("refunded supplier order caused delivery")
			}
		})
	}
}
