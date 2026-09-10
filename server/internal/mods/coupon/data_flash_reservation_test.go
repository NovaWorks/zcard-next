package coupon

import (
	"context"
	"errors"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"sync"
	"testing"
	"time"
)

func TestFlashReservationConcurrentDoesNotCountUnpaidAsSold(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	seedProduct(t, d, 1, 100)
	fs, err := r.CreateFlash(ctx, 1, 0, 50, time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour), 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 50)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- r.Reserve(ctx, fs.ID, 1) }()
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
		t.Fatal(success, busy)
	}
	got := d.Client.FlashSale.GetX(ctx, fs.ID)
	if got.SoldQty != 0 || got.ReservedQty != 10 {
		t.Fatalf("unpaid=%+v", got)
	}
	offer, err := r.Active(ctx, 1, 0)
	if err != nil || offer.Remaining != 10 {
		t.Fatal("unpaid changed advertised quantity", err)
	}
	if err := r.Consume(ctx, fs.ID, 1); err == nil {
		t.Fatal("direct consume stole reserved unit")
	}
	for i := 0; i < 5; i++ {
		if err := r.Confirm(ctx, fs.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := r.Release(ctx, fs.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	got = d.Client.FlashSale.GetX(ctx, fs.ID)
	if got.SoldQty != 5 || got.ReservedQty != 0 {
		t.Fatalf("settled=%+v", got)
	}
	if err := r.Release(ctx, fs.ID, 1); err == nil {
		t.Fatal("reservation underflow")
	}
	if err := r.Confirm(ctx, fs.ID, 1); err == nil {
		t.Fatal("paid without reservation")
	}
}
