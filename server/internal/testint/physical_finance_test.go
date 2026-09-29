//go:build integration

package testint

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/affiliate"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"sync"
	"testing"
	"time"
)

func TestPhysicalFinanceMySQL(t *testing.T) { physicalFinance(MySQL(t)) }
func TestPhysicalFinancePG(t *testing.T)    { physicalFinance(PG(t)) }
func physicalFinance(h *Harness) {
	ctx := context.Background()
	d := h.Data
	t := h.T
	repo := affiliate.NewCommissionRepo(d)
	w := wallet.NewWalletRepoImpl(d)
	w.SetCommissionConsumer(repo)
	wp := wallet.ProvidePortWallet(w)
	service := affiliate.NewAffiliateService(repo, wp, nil, nil, nil)
	if e := wp.CreditInTx(ctx, walletport.Entry{UserID: 7, Direction: "in", Type: "commission", Amount: 200, Reference: "initial-review-commission"}); e != nil {
		t.Fatal(e)
	}
	var ids []uint64
	for i := 0; i < 2; i++ {
		o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("FINANCE-%d", i)).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(1000).SaveX(ctx)
		ids = append(ids, o.ID)
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetPaidAmount(1000).SetRefundedAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
		d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(500).SetChannel("wallet").SetStatus("succeeded").SaveX(ctx)
		d.Client.AffiliateCommission.Create().SetOrderID(o.ID).SetReferrerID(7).SetBuyerID(8).SetTier(1).SetRate(1000).SetBaseAmount(1000).SetAmount(100).SetStatus("available").SaveX(ctx)
	}
	withdrawal := d.Client.Withdrawal.Create().SetUserID(7).SetAmount(50).SetFee(0).SetMethod(map[string]any{"type": "test"}).SetStatus("approved").SaveX(ctx)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var e error
			for i := 0; i < 8; i++ {
				e = fn()
				if e == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			errs <- e
		}()
	}
	for _, id := range ids {
		id := id
		run(func() error {
			return service.OnOrderRefunded(ctx, events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"commerce_version":1}`, id))})
		})
	}
	run(func() error { _, e := w.PayWithdrawal(ctx, withdrawal.ID, "receipt"); return e })
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if n := d.Client.WalletAccount.Query().OnlyX(ctx).Available; n != 100 {
		t.Fatalf("refund wallet balance=%d", n)
	}
	stats, e := repo.StatsByUser(ctx, 7)
	if e != nil {
		t.Fatal(e)
	}
	if stats.AvailableCents != 50 || stats.WithdrawnCents != 50 {
		t.Fatalf("commission conservation: %+v", stats)
	}
	if _, e := w.PayWithdrawal(ctx, withdrawal.ID, "duplicate"); e == nil {
		t.Fatal("paid withdrawal replay consumed commission again")
	}
	for _, n := range []int64{10, 10} {
		if e := repo.ConsumeAvailableFIFO(ctx, 7, n); e != nil {
			t.Fatal(e)
		}
	}
	stats, e = repo.StatsByUser(ctx, 7)
	if e != nil || stats.AvailableCents != 30 || stats.WithdrawnCents != 70 {
		t.Fatalf("repeated split conservation: %+v %v", stats, e)
	}
}
