//go:build integration

package procurement

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
	"sync"
	"testing"
)

func TestReusableSQLDialects(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *testint.Harness{"mysql": testint.MySQL, "postgres": testint.PG} {
		t.Run(name, func(t *testing.T) {
			h := open(t)
			s, d, src, gw, delivery := seedReusableFixture(t, NewProcureRepo(h.Data), h.Data)
			ctx := context.Background()
			first := reusableBuyer(t, d, src, 0)
			for i := 1; i < 12; i++ {
				reusableBuyer(t, d, src, i)
			}
			gw.entered = make(chan struct{}, 1)
			gw.release = make(chan struct{})
			done := make(chan error, 1)
			go func() { done <- s.processReusable(ctx, src.ID) }()
			<-gw.entered
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if e := s.processReusable(ctx, src.ID); e != nil {
						t.Error(e)
					}
				}()
			}
			wg.Wait()
			d.Client.Order.UpdateOneID(first.OrderID).SetStatus("refunded").ExecX(ctx)
			close(gw.release)
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if gw.calls.Load() != 1 || d.Client.OrderDelivery.Query().CountX(ctx) != 11 {
				t.Fatal("duplicate procurement or refund blocked other buyers")
			}
			// Concurrent delivery workers must obey a shared cap even with late-paid orders.
			d.Client.ProductDeliverySource.UpdateOneID(src.ID).SetMaxDeliveries(12).ExecX(ctx)
			a := reusableBuyer(t, d, src, 20)
			b := reusableBuyer(t, d, src, 21)
			wg.Add(2)
			go func() {
				defer wg.Done()
				if e := delivery.DeliverReusable(ctx, a.OrderID, a.ID, src.ID); e != nil {
					t.Error(e)
				}
			}()
			go func() {
				defer wg.Done()
				if e := delivery.DeliverReusable(ctx, b.OrderID, b.ID, src.ID); e != nil {
					t.Error(e)
				}
			}()
			wg.Wait()
			if d.Client.OrderDelivery.Query().CountX(ctx) != 12 || d.Client.ProductDeliverySource.GetX(ctx, src.ID).DeliveredCount != 12 {
				t.Fatal("delivery cap exceeded")
			}
		})
	}
}
