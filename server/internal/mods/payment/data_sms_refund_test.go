package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"testing"
	"time"
)

func seedSMSRefund(t *testing.T, version int32) (*data.Data, *PaymentRepoImpl, *ent.SMSIntent) {
	t.Helper()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("sms-refund").SetUserID(1).SetStatus("paid").SetBaseCurrency("CNY").SetTotalAmount(999).SetCommerceVersion(version).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(999).SetAmount(999).SetPaidAmount(999).SetCost(123).SetDeliveryKind("sms_activation").SetFulfillmentType("upstream").SetFulfillmentStatus("pending").SaveX(ctx)
	row := d.Client.SMSIntent.Create().SetOrderID(o.ID).SetOrderItemID(it.ID).SetUserID(1).SetConnectionID(1).SetConnectionIdentity("original").SetRequestNo("sms_original").SetRequestJSON("{}").SetRequestHash("hash").SetUpstreamOrderID("original").SetChargedAmount(123).SetState("canceled").SetSettlementState("refunded").SetRefundedAmount(123).SetRefundReference("original-refund").SetRetailRefundState("pending").SetPhase("refund").SaveX(ctx)
	return d, r, row
}
func TestSMSRetailRefundUsesRemainingRetailNotSupplyCost(t *testing.T) {
	for _, version := range []int32{0, 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			d, r, row := seedSMSRefund(t, version)
			ctx := context.Background()
			id, e := r.RefundSMS(ctx, row.ID)
			if e != nil {
				t.Fatal(e)
			}
			again, e := r.RefundSMS(ctx, row.ID)
			if e != nil || again != id {
				t.Fatal("idempotency", e)
			}
			if got := d.Client.WalletAccount.Query().OnlyX(ctx).Available; got != 999 {
				t.Fatalf("credited %d, want retail 999", got)
			}
			rf := d.Client.RefundOrder.Query().OnlyX(ctx)
			if rf.RequestKey != fmt.Sprintf("sms_retail_refund:%d", row.OrderItemID) || rf.Amount != 999 {
				t.Fatal("stable refund reference missing")
			}
			if d.Client.WalletTransaction.Query().CountX(ctx) != 1 || d.Client.SMSIntent.GetX(ctx, row.ID).Phase != "done" {
				t.Fatal("duplicate ledger or task not closed")
			}
		})
	}
}
func TestSMSRefundRequiresVerifiedFullCostAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"partial_cost", "missing_reference", "unknown_creation", "outbox_failure"} {
		t.Run(scenario, func(t *testing.T) {
			d, r, row := seedSMSRefund(t, 0)
			ctx := context.Background()
			switch scenario {
			case "partial_cost":
				d.Client.SMSIntent.UpdateOneID(row.ID).SetRefundedAmount(12).ExecX(ctx)
			case "missing_reference":
				d.Client.SMSIntent.UpdateOneID(row.ID).SetRefundReference("").ExecX(ctx)
			case "unknown_creation":
				d.Client.SMSIntent.UpdateOneID(row.ID).SetSettlementState("paid").ExecX(ctx)
			case "outbox_failure":
				r.outbox = refundFailWriter{}
			}
			if _, e := r.RefundSMS(ctx, row.ID); e == nil {
				t.Fatal("invalid refund accepted")
			}
			if d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
				t.Fatal("refund escaped transaction")
			}
		})
	}
}
func TestSMSRefundAfterManualPartial(t *testing.T) {
	d, r, row := seedSMSRefund(t, 0)
	ctx := context.Background()
	if _, e := r.RefundToWallet(ctx, row.OrderID, 100, refundPtr(0), "manual", 7); e != nil {
		t.Fatal(e)
	}
	if _, e := r.RefundSMS(ctx, row.ID); e != nil {
		t.Fatal(e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 999 {
		t.Fatal("over-refunded")
	}
}

func TestSMSPaymentRejectsExternalClosedOrChangedAccount(t *testing.T) {
	for _, kind := range []string{"external", "closed", "changed_account", "currency", "fee"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("ZCARD_SMS_SALES_ENABLED", "true")
			d, r, row := seedSMSRefund(t, 0)
			ctx := context.Background()
			o := d.Client.Order.UpdateOneID(row.OrderID).SetStatus("pending_payment").SetExpiredAt(time.Now().Add(time.Hour)).SaveX(ctx)
			c := d.Client.SupplyConnection.Create().SetName("original").SetDriver("zcard").SetBaseURL("https://example.invalid").SetCredentials([]byte("original")).SaveX(ctx)
			raw, _ := json.Marshal(data.FrozenSMS{ConnectionID: c.ID, Identity: data.SMSConnectionIdentity(c)})
			d.Client.OrderItem.UpdateOneID(row.OrderItemID).SetSmsPurchaseSnapshot(string(raw)).ExecX(ctx)
			channel := "balance"
			switch kind {
			case "external":
				channel = "epay"
			case "closed":
				t.Setenv("ZCARD_SMS_SALES_ENABLED", "false")
			case "changed_account":
				d.Client.SupplyConnection.UpdateOneID(c.ID).SetCredentials([]byte("different")).ExecX(ctx)
			case "currency":
				d.Client.Order.UpdateOneID(o.ID).SetBaseCurrency("USD").ExecX(ctx)
			case "fee":
				d.Client.PaymentChannel.Update().Where(paymentchannel.Code("balance")).SetFee(10).ExecX(ctx)
			}
			if _, e := r.CreatePayment(ctx, o.ID, channel, 999, "sms-payment"); e == nil {
				t.Fatal("unsafe SMS payment accepted")
			}
			if d.Client.Payment.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
				t.Fatal("rejected payment left financial effects")
			}
		})
	}
}
