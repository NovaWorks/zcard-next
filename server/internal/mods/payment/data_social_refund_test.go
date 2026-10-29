package payment

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"testing"
)

func socialRefundFixture(t *testing.T) (*data.Data, *PaymentRepoImpl, *ent.SocialTask) {
	t.Helper()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("social-refund").SetUserID(1).SetStatus("paid").SetBaseCurrency("CNY").SetTotalAmount(999).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetDeliveryKind("social_service").SetQuantity(1).SetUnitPrice(999).SetAmount(999).SetPaidAmount(999).SetFulfillmentType("upstream").SaveX(ctx)
	d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("wallet").SetAmount(999).SetChargedAmount(999).SetStatus("success").SaveX(ctx)
	row := d.Client.SocialTask.Create().SetID("retail-task").SetRole("retail").SetOrderID(o.ID).SetOrderItemID(it.ID).SetUserID(1).SetConnectionID(1).SetQuoteID("q").SetSchemaRevision("s").SetRequestNo("original-request").SetRequestHash("hash").SetUpstreamProductID("p").SetUpstreamOrderID("A-task").SetChargedAmount(100).SetRetailPaidAmount(999).SetState("partial").SetSettlementState("partially_refunded").SetRefundedAmount(50).SetRefundReference("A-refund-1").SetPhase("refund").SaveX(ctx)
	return d, r, row
}
func TestSocialRetailPartialRefundCumulativeRemainderAndReplay(t *testing.T) {
	d, r, row := socialRefundFixture(t)
	ctx := context.Background()
	first, e := r.RefundSocial(ctx, row.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.RefundSocial(ctx, row.ID)
	if e != nil || again != first {
		t.Fatal("same refund replay", e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 499 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 {
		t.Fatal("partial amount/replay wrong")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetRefundedAmount(100).SetRefundReference("A-refund-2").SetSettlementState("refunded").SetPhase("refund").ExecX(ctx)
	if _, e = r.RefundSocial(ctx, row.ID); e != nil {
		t.Fatal(e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 999 || d.Client.WalletTransaction.Query().CountX(ctx) != 2 || d.Client.Order.GetX(ctx, row.OrderID).Status != "refunded" {
		t.Fatal("full remainder or native refund state wrong")
	}
	if _, e = r.RefundSocial(ctx, row.ID); e != nil || d.Client.WalletTransaction.Query().CountX(ctx) != 2 {
		t.Fatal("full refund duplicated", e)
	}
}
func TestSocialRefundRequiresReferenceAndAtomicOutbox(t *testing.T) {
	for _, kind := range []string{"missing-reference", "unconfirmed", "overpaid", "outbox"} {
		t.Run(kind, func(t *testing.T) {
			d, r, row := socialRefundFixture(t)
			ctx := context.Background()
			switch kind {
			case "missing-reference":
				d.Client.SocialTask.UpdateOneID(row.ID).SetRefundReference("").ExecX(ctx)
			case "unconfirmed":
				d.Client.SocialTask.UpdateOneID(row.ID).SetSettlementState("review").ExecX(ctx)
			case "overpaid":
				d.Client.SocialTask.UpdateOneID(row.ID).SetRefundedAmount(101).ExecX(ctx)
			case "outbox":
				r.outbox = nil
			}
			if _, e := r.RefundSocial(ctx, row.ID); e == nil {
				t.Fatal("unverified refund credited")
			}
			if d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
				t.Fatal("failed refund escaped transaction")
			}
		})
	}
}
func TestSocialManualRefundCannotExceedConfirmedEntitlement(t *testing.T) {
	d, r, row := socialRefundFixture(t)
	ctx := context.Background()
	expected := int64(0)
	if _, e := r.RefundToWallet(ctx, row.OrderID, 999, &expected, "manual", 2); e == nil {
		t.Fatal("manual refund bypassed confirmed supply limit")
	}
	if d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
		t.Fatal("rejected manual refund credited")
	}
}
