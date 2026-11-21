package payment

import (
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

func rejectedCardFixture(t *testing.T) (*data.Data, *PaymentRepoImpl, *ent.Order, *ent.ProcurementOrder) {
	t.Helper()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = refundThreadWriter{}
	ctx := t.Context()
	o := d.Client.Order.Create().SetOrderNo("REJECTED-CARD").SetUserID(1).SetBaseCurrency("CNY").SetStatus("fulfilling").SetTotalAmount(500).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetProductName("card").SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetFulfillmentType("upstream").SetDeliveryKind("card").SaveX(ctx)
	c := d.Client.SupplyConnection.Create().SetName("ZCard").SetDriver("zcard").SetBaseURL("https://supply.example").SetCredentials([]byte("encrypted-fixture")).SaveX(ctx)
	po := d.Client.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(c.ID).SetDedupeKey("rejected-fixture").SetStatus("refunding").SetLastError(supplyport.ErrUpstreamNoStock.Error()).SaveX(ctx)
	d.Client.ProcurementItem.Create().SetProcurementID(po.ID).SetUpstreamSku("1").SetQuantity(1).SaveX(ctx)
	d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("wallet").SetDriverSnapshot("wallet").SetAmount(500).SetChargedAmount(500).SetStatus("success").SaveX(ctx)
	return d, r, o, po
}

func TestRejectedCardWalletRefundSettlesOnce(t *testing.T) {
	d, r, o, po := rejectedCardFixture(t)
	for range 2 {
		if e := r.RefundRejectedCard(t.Context(), po.ID); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.Order.GetX(t.Context(), o.ID).Status != "refunded" || d.Client.ProcurementOrder.GetX(t.Context(), po.ID).Status != "refunded" || d.Client.WalletAccount.Query().OnlyX(t.Context()).Available != 500 || d.Client.RefundOrder.Query().CountX(t.Context()) != 1 || d.Client.WalletTransaction.Query().CountX(t.Context()) != 1 {
		t.Fatal("refund was not committed exactly once")
	}
}

func TestRejectedCardRefundRequiresUnacceptedPaidWalletProof(t *testing.T) {
	for _, name := range []string{"unknown", "accepted", "receipt", "gateway", "fee", "guest", "multiple-items", "delivered", "manual", "unproven-refunded", "outbox-failure"} {
		t.Run(name, func(t *testing.T) {
			d, r, o, po := rejectedCardFixture(t)
			ctx := t.Context()
			switch name {
			case "unknown":
				d.Client.ProcurementOrder.UpdateOneID(po.ID).SetLastError(supplyport.ErrUpstreamUncertain.Error()).ExecX(ctx)
			case "accepted":
				d.Client.ProcurementOrder.UpdateOneID(po.ID).SetUpstreamOrderID("provider-accepted").ExecX(ctx)
			case "receipt":
				part := d.Client.ProcurementItem.Query().OnlyX(ctx)
				d.Client.ProcurementItem.UpdateOneID(part.ID).SetReceivedContent([]string{"cipher-receipt"}).ExecX(ctx)
			case "gateway":
				paid := d.Client.Payment.Query().OnlyX(ctx)
				d.Client.Payment.UpdateOneID(paid.ID).SetDriverSnapshot("external-gateway").ExecX(ctx)
			case "fee":
				paid := d.Client.Payment.Query().OnlyX(ctx)
				d.Client.Payment.UpdateOneID(paid.ID).SetFee(1).ExecX(ctx)
			case "guest":
				d.Client.Order.UpdateOneID(o.ID).SetUserID(0).ExecX(ctx)
			case "multiple-items":
				d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(2).SetProductName("other").SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetFulfillmentType("upstream").SaveX(ctx)
			case "delivered":
				d.Client.OrderItem.UpdateOneID(po.OrderItemID).SetFulfillmentStatus("delivered").ExecX(ctx)
			case "manual":
				d.Client.ProcurementOrder.UpdateOneID(po.ID).SetFailStrategy("manual").ExecX(ctx)
			case "unproven-refunded":
				d.Client.ProcurementOrder.UpdateOneID(po.ID).SetStatus("refunded").ExecX(ctx)
			case "outbox-failure":
				r.outbox = refundFailWriter{}
			}
			if e := r.RefundRejectedCard(ctx, po.ID); e == nil {
				t.Fatal("unproven refund accepted")
			}
			if d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 || d.Client.Order.GetX(ctx, o.ID).Status != "fulfilling" {
				t.Fatal("failed guard credited money or changed order")
			}
		})
	}
}
