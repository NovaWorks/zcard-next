package payment

import (
	"context"
	"strings"
	"testing"
)

func TestDiscountedOrderRejectsUnderpayment(t *testing.T) {
	d, repo, _, _, lifecycle, _ := newCallbackEnv(t)
	ctx := context.Background()
	o, p := seedPendingOrder(t, d, "epay", 3528) // 36 yuan at 98% payable.
	fact := CallbackFact{Channel: "epay", OrderNo: o.OrderNo, Amount: 72, Currency: "CNY", Success: true}
	if err := repo.HandleCallback(ctx, p.ID, fact); err == nil || !strings.Contains(err.Error(), "AMOUNT_MISMATCH") {
		t.Fatalf("underpayment accepted: %v", err)
	}
	if len(lifecycle.markPaidCalls) != 0 || string(d.Client.Payment.GetX(ctx, p.ID).Status) != "pending" {
		t.Fatal("underpayment advanced fulfillment")
	}
	fact.Amount = 3528
	if err := repo.HandleCallback(ctx, p.ID, fact); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.markPaidCalls) != 1 || lifecycle.markPaidCalls[0] != o.OrderNo || string(d.Client.Payment.GetX(ctx, p.ID).Status) != "success" {
		t.Fatal("correct payment not settled")
	}
}
