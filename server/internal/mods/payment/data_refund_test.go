package payment

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

func refundPtr(v int64) *int64 { return &v }
func TestWalletRefundAtomicPartialAndRetry(t *testing.T) {
	d, r, _, w, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("R-1").SetUserID(1).SetStatus(order.StatusPaid).SetTotalAmount(1000).SaveX(ctx)
	old := d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(1000).SetChannel(refundorder.ChannelWallet).SaveX(ctx)
	for _, amount := range []int64{-1, 0, 1001} {
		if _, err := r.RefundToWallet(ctx, o.ID, amount, refundPtr(0), "", 2); err == nil {
			t.Fatal("invalid refund accepted")
		}
	}
	if _, err := r.RefundToWallet(ctx, o.ID, 1000, nil, "", 2); err == nil {
		t.Fatal("missing confirmation accepted")
	}
	rf, err := r.RefundToWallet(ctx, o.ID, 400, refundPtr(0), "partial", 2)
	if err != nil || rf.Status != refundorder.StatusSucceeded {
		t.Fatalf("refund %+v %v", rf, err)
	}
	if _, err := r.RefundToWallet(ctx, o.ID, 400, refundPtr(0), "retry", 2); err == nil {
		t.Fatal("duplicate refund accepted")
	}
	if _, err := r.RefundToWallet(ctx, o.ID, 601, refundPtr(400), "excess", 2); err == nil {
		t.Fatal("excess refund accepted")
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusPaid || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 400 {
		t.Fatal("partial state or balance incorrect")
	}
	if d.Client.RefundOrder.GetX(ctx, old.ID).Status != refundorder.StatusFailed {
		t.Fatal("old unexecuted refund not superseded")
	}
	_, err = r.RefundToWallet(ctx, o.ID, 600, refundPtr(400), "full", 2)
	if err != nil {
		t.Fatal(err)
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusRefunded || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 1000 || d.Client.WalletTransaction.Query().CountX(ctx) != 2 {
		t.Fatal("full refund incorrect")
	}
	if !w.has(events.OrderRefunded) {
		t.Fatal("refund event missing")
	}
	if _, err := r.RefundToWallet(ctx, o.ID, 1, refundPtr(1000), "", 2); err == nil {
		t.Fatal("terminal order refunded again")
	}
}

type refundFailWriter struct{}

func (refundFailWriter) Write(context.Context, string, string, string, string, json.RawMessage) error {
	return errors.New("outbox offline")
}
func TestWalletRefundRollbackAndEligibility(t *testing.T) {
	for _, failure := range []string{"ledger", "outbox"} {
		t.Run(failure, func(t *testing.T) {
			d, r, _, _, _, _ := newCallbackEnv(t)
			ctx := context.Background()
			o := d.Client.Order.Create().SetOrderNo("FAIL").SetUserID(1).SetStatus(order.StatusCompleted).SetTotalAmount(500).SaveX(ctx)
			d.Client.WalletAccount.Create().SetUserID(1).SetAvailable(100).SaveX(ctx)
			if failure == "ledger" {
				d.Client.WalletTransaction.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("ledger offline") })
				})
			} else {
				r.outbox = refundFailWriter{}
			}
			if _, err := r.RefundToWallet(ctx, o.ID, 500, refundPtr(0), "", 1); err == nil {
				t.Fatal("failure hidden")
			}
			if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusCompleted || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 100 || d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 || d.Client.OrderStatusEvent.Query().CountX(ctx) != 0 {
				t.Fatal("partial financial commit")
			}
		})
	}
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	for _, status := range []order.Status{order.StatusPendingPayment, order.StatusCanceled, order.StatusExpired, order.StatusRefunded} {
		o := d.Client.Order.Create().SetOrderNo(string(status)).SetUserID(1).SetStatus(status).SetTotalAmount(100).SaveX(ctx)
		if _, err := r.RefundToWallet(ctx, o.ID, 100, refundPtr(0), "", 1); err == nil {
			t.Fatal("invalid status refunded")
		}
	}
	guest := d.Client.Order.Create().SetOrderNo("guest").SetStatus(order.StatusPaid).SetTotalAmount(100).SaveX(ctx)
	if _, err := r.RefundToWallet(ctx, guest.ID, 100, refundPtr(0), "", 1); err == nil {
		t.Fatal("guest credited")
	}
}
func TestWalletRefundConcurrent(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	r.outbox = refundThreadWriter{}
	o := d.Client.Order.Create().SetOrderNo("CONCURRENT").SetUserID(1).SetStatus(order.StatusPaid).SetTotalAmount(500).SaveX(ctx)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; r.RefundToWallet(ctx, o.ID, 500, refundPtr(0), "", 1) }()
	}
	close(start)
	wg.Wait()
	if d.Client.RefundOrder.Query().Where(refundorder.StatusEQ(refundorder.StatusSucceeded)).CountX(ctx) == 0 {
		if _, err := r.RefundToWallet(ctx, o.ID, 500, refundPtr(0), "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 500 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 || d.Client.RefundOrder.Query().CountX(ctx) != 1 {
		t.Fatal("concurrent duplicate credit")
	}
}

type refundThreadWriter struct{}

func (refundThreadWriter) Write(context.Context, string, string, string, string, json.RawMessage) error {
	return nil
}
