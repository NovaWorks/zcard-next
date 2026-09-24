//go:build integration

package testint

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

type procurementTestQueue struct{}

func (procurementTestQueue) Enabled() bool                             { return true }
func (procurementTestQueue) Enqueue(context.Context, queue.Task) error { return nil }
func TestProcurementRecoveryMySQL(t *testing.T)                        { runProcurementRecovery(MySQL(t)) }
func TestProcurementRecoveryPG(t *testing.T)                           { runProcurementRecovery(PG(t)) }
func runProcurementRecovery(h *Harness) {
	t := h.T
	ctx := context.Background()
	c := h.Data.Client
	p := c.Product.Create().SetName("upstream").SetSlug("upstream").SetPrice(100).SaveX(ctx)
	o := c.Order.Create().SetOrderNo("RECOVERY").SetStatus("paid").SetTotalAmount(100).SetPaidAt(time.Now().UTC()).SaveX(ctx)
	it := c.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("upstream").SaveX(ctx)
	repo := procurement.NewProcureRepo(h.Data)
	po, err := repo.CreatePending(ctx, it.ID, 1, "P1", 1, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	delivery := fulfillment.NewDeliveryRepoImpl(h.Data, cipher, nil, nil)
	svc := procurement.NewProcureService(repo, nil, nil, cipher, delivery, nil, data.NewOutboxWriter(h.Data), procurementTestQueue{}, slog.Default())
	fail := true
	c.OrderDelivery.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if fail {
				return nil, errors.New("injected failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	result := &supplyport.UpstreamCallbackResult{Status: "delivered", Cards: []string{"CARD"}, Amount: 100}
	if err := svc.HandleUpstreamCallback(ctx, po.ID, result); err == nil {
		t.Fatal("failure swallowed")
	}
	if c.ProcurementOrder.GetX(ctx, po.ID).Status != procurementorder.StatusPolling || c.Card.Query().CountX(ctx) != 0 || c.OrderDelivery.Query().CountX(ctx) != 0 {
		t.Fatal("partial commit")
	}
	fail = false
	// Race local recovery with manual intervention. Either may win, never both deliveries.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(manual bool) {
			defer wg.Done()
			<-start
			if manual {
				_ = repo.MarkManual(ctx, po.ID, "verified")
			} else {
				_ = svc.PollOne(ctx, po.ID)
			}
		}(i%2 == 0)
	}
	close(start)
	wg.Wait()
	current := c.ProcurementOrder.GetX(ctx, po.ID)
	if current.Status == procurementorder.StatusManual {
		if err := delivery.ManualDeliver(ctx, o.OrderNo, "MANUAL", "", "", 1, it.ID); err != nil {
			t.Fatal(err)
		}
	} else if err := svc.PollOne(ctx, po.ID); err != nil {
		t.Fatal(err)
	}
	if c.OrderDelivery.Query().CountX(ctx) != 1 || c.Card.Query().CountX(ctx) != 1 {
		t.Fatal("concurrent recovery duplicated delivery")
	}
	if c.ProcurementOrder.GetX(ctx, po.ID).Status != procurementorder.StatusFulfilled || string(c.Order.GetX(ctx, o.ID).Status) != "delivered" {
		t.Fatal("completion mismatch")
	}
	// A historic missing procurement is visible after patrol, without any supplier gateway.
	o2 := c.Order.Create().SetOrderNo("MISSING").SetStatus("paid").SetTotalAmount(100).SetPaidAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	it2 := c.OrderItem.Create().SetOrderID(o2.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("upstream").SaveX(ctx)
	svc.Patrol(ctx)
	missing, err := repo.GetByOrderItem(ctx, it2.ID)
	if err != nil || missing.Status != procurementorder.StatusManual {
		t.Fatalf("missing procurement not found: %v", err)
	}
}
