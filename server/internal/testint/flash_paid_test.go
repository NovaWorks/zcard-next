//go:build integration

package testint

import (
	"context"
	"errors"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"sync"
	"testing"
	"time"
)

func TestFlashPaidMySQL(t *testing.T) { runFlashPaid(MySQL(t)) }
func TestFlashPaidPG(t *testing.T)    { runFlashPaid(PG(t)) }
func runFlashPaid(h *Harness) {
	t := h.T
	ctx := context.Background()
	repo := coupon.NewCouponRepoImpl(h.Data)
	p := h.Data.Client.Product.Create().SetName("flash integration").SetSlug("flash-integration").SetStockType("url").SetPrice(100).SaveX(ctx)
	fs, err := repo.CreateFlash(ctx, p.ID, 0, 50, time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour), 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- data.Tx(ctx, h.Data, func(tx context.Context) error { return repo.Reserve(tx, fs.ID, 1) })
		}()
	}
	wg.Wait()
	close(results)
	success, busy := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, port.ErrFlashReserved) {
			busy++
		} else {
			t.Fatal(err)
		}
	}
	if success != 10 || busy != 40 {
		t.Fatal("oversold reservation", success, busy)
	}
	got := h.Data.Client.FlashSale.GetX(ctx, fs.ID)
	if got.SoldQty != 0 || got.ReservedQty != 10 {
		t.Fatalf("unpaid: %+v", got)
	}
	err = data.Tx(ctx, h.Data, func(tx context.Context) error {
		if err := repo.Confirm(tx, fs.ID, 1); err != nil {
			return err
		}
		return errors.New("failed callback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	got = h.Data.Client.FlashSale.GetX(ctx, fs.ID)
	if got.SoldQty != 0 || got.ReservedQty != 10 {
		t.Fatal("callback failure consumed quota")
	}
	results = make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- data.Tx(ctx, h.Data, func(tx context.Context) error {
				if i%2 == 0 {
					return repo.Confirm(tx, fs.ID, 1)
				}
				return repo.Release(tx, fs.ID, 1)
			})
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	got = h.Data.Client.FlashSale.GetX(ctx, fs.ID)
	if got.SoldQty != 5 || got.ReservedQty != 0 {
		t.Fatalf("settled %+v", got)
	}
	offer, err := repo.Active(ctx, p.ID, 0)
	if err != nil || offer.Remaining != 5 {
		t.Fatal("incorrect remaining", err)
	}
	// Run actual order state transitions against the migrated database too.
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	uc := &ordermod.OrderUsecase{Data: h.Data, Inv: inventory.NewCardRepoImpl(h.Data, cipher), Gen: gen, Flash: repo}
	makeOrder := func(key string) *ordermod.CreateOrderResult {
		t.Helper()
		o, err := uc.CreateOrder(ctx, ordermod.CreateOrderInput{UserID: 7, QueryPassword: "abcd", IdempotencyKey: key, Items: []ordermod.OrderItemInput{{ProductID: p.ID, Quantity: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	first := makeOrder("first")
	second := makeOrder("second")
	got = h.Data.Client.FlashSale.GetX(ctx, fs.ID)
	if got.SoldQty != 5 || got.ReservedQty != 2 {
		t.Fatal("create consumed stock")
	}
	results = make(chan error, 2)
	go func() { results <- uc.MarkPaid(ctx, first.OrderNo) }()
	go func() { results <- uc.CancelOrder(ctx, first.OrderNo, "race cancel", "system", 0) }()
	succeeded := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatal("payment/cancel race must have exactly one winner", succeeded)
	}
	if err := uc.CancelOrder(ctx, second.OrderNo, "release", "system", 0); err != nil {
		t.Fatal(err)
	}
	if err := uc.CancelOrder(ctx, second.OrderNo, "repeat release", "system", 0); err != nil {
		t.Fatal(err)
	}
	got = h.Data.Client.FlashSale.GetX(ctx, fs.ID)
	if got.ReservedQty != 0 || (got.SoldQty != 5 && got.SoldQty != 6) {
		t.Fatalf("race damaged quota: %+v", got)
	}
}
