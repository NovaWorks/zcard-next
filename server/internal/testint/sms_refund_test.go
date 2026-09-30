//go:build integration

package testint

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"sync"
	"testing"
)

func TestSMSRefundMySQL(t *testing.T) {
	for _, v := range []int32{0, 1} {
		t.Run(fmt.Sprint(v), func(t *testing.T) { smsRefundConcurrent(MySQL(t), v) })
	}
}
func TestSMSRefundPG(t *testing.T) {
	for _, v := range []int32{0, 1} {
		t.Run(fmt.Sprint(v), func(t *testing.T) { smsRefundConcurrent(PG(t), v) })
	}
}
func smsRefundConcurrent(h *Harness, version int32) {
	t, d, ctx := h.T, h.Data, context.Background()
	r := payment.NewPaymentRepoImpl(d, nil, nil, nil, wallet.ProvidePortWallet(wallet.NewWalletRepoImpl(d)), nil, data.NewOutboxWriter(d), nil, nil, nil)
	o := d.Client.Order.Create().SetOrderNo("SMS-CONCURRENT").SetUserID(1).SetStatus("paid").SetBaseCurrency("CNY").SetTotalAmount(999).SetCommerceVersion(version).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(999).SetAmount(999).SetPaidAmount(999).SetCost(123).SetDeliveryKind("sms_activation").SetFulfillmentType("upstream").SetFulfillmentStatus("pending").SaveX(ctx)
	row := d.Client.SMSIntent.Create().SetOrderID(o.ID).SetOrderItemID(it.ID).SetUserID(1).SetConnectionID(1).SetConnectionIdentity("original").SetRequestNo("sms_original").SetRequestJSON("{}").SetRequestHash("hash").SetUpstreamOrderID("original").SetChargedAmount(123).SetState("canceled").SetSettlementState("refunded").SetRefundedAmount(123).SetRefundReference("original-refund").SetRetailRefundState("pending").SetPhase("refund").SaveX(ctx)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, _ = r.RefundSMS(ctx, row.ID) }()
	}
	close(start)
	wg.Wait()
	id, e := r.RefundSMS(ctx, row.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.RefundSMS(ctx, row.ID)
	if e != nil || again != id {
		t.Fatal("unstable receipt", e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 999 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 || d.Client.RefundOrder.Query().CountX(ctx) != 1 {
		t.Fatal("concurrent refund overcredited or duplicated")
	}
	if e = data.ApplyMigrations(ctx, d.DB, d.Dialect, h.DSN); e != nil {
		t.Fatal("repeat migrations", e)
	}
}
