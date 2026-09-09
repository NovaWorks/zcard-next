//go:build integration

package testint

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
)

func TestPaymentCancelRaceMySQL(t *testing.T) { runPaymentCancelRace(MySQL(t)) }
func TestPaymentCancelRacePG(t *testing.T)    { runPaymentCancelRace(PG(t)) }

func runPaymentCancelRace(h *Harness) {
	t := h.T
	ctx := context.Background()
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.NewCardRepoImpl(h.Data, cipher)
	uc := &ordermod.OrderUsecase{Data: h.Data, Inv: inv}
	product := h.Data.Client.Product.Create().SetName("race").SetSlug("race").SaveX(ctx)
	for i := 0; i < 30; i++ {
		o := h.Data.Client.Order.Create().SetOrderNo(fmt.Sprintf("race-%d", i)).SetExpiredAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
		c := h.Data.Client.Card.Create().SetProductID(product.ID).SetOrderID(o.ID).SetStatus(card.StatusReserved).SetLockedAt(time.Now().UTC()).SetContent([]byte("test")).SetContentHash(fmt.Sprintf("race-%d", i)).SaveX(ctx)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var payErr, cancelErr error
		go func() {
			defer wg.Done()
			<-start
			payErr = data.Tx(ctx, h.Data, func(tx context.Context) error { return uc.MarkPaid(tx, o.OrderNo) })
		}()
		go func() { defer wg.Done(); <-start; cancelErr = uc.CancelOrder(ctx, o.OrderNo, "race", "system", 0) }()
		close(start)
		wg.Wait()
		if payErr == nil && cancelErr == nil {
			t.Fatal("both state transitions succeeded")
		}
		got := h.Data.Client.Order.GetX(ctx, o.ID)
		stock := h.Data.Client.Card.GetX(ctx, c.ID)
		switch got.Status {
		case order.StatusPaid:
			if payErr != nil || !got.ClosedAt.IsZero() || stock.Status != card.StatusReserved {
				t.Fatalf("paid order lost stock: %+v", got)
			}
		case order.StatusCanceled:
			if cancelErr != nil || !got.PaidAt.IsZero() || stock.Status != card.StatusAvailable || stock.OrderID != 0 {
				t.Fatalf("canceled order retained stock: %+v", got)
			}
		default:
			t.Fatalf("neither transition committed pay=%v cancel=%v", payErr, cancelErr)
		}
	}
}
