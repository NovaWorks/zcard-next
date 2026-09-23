package order

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// Establish two old snapshots before either buyer claims the single service quota.
func TestManualQuotaMySQLRepeatableRead(t *testing.T) {
	source := os.Getenv("ZCARD_SERVICES_QUOTA_MYSQL_DSN")
	if source == "" {
		t.Skip("isolated MySQL not configured")
	}
	d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "mysql", Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx := context.Background()
	if err = d.Client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	p := d.Client.Product.Create().SetName("manual quota").SetSlug("manual-quota").SetPrice(500).SetFulfillmentMode("manual").SetManualStock(1).SaveX(ctx)
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	uc := &OrderUsecase{Data: d, Gen: gen, Inv: fakeInventory{}}
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	start := make(chan struct{})
	var succeeded atomic.Int32
	for i := 0; i < 2; i++ {
		go func() {
			defer done.Done()
			err := data.Tx(ctx, d, func(tx context.Context) error {
				_, err := data.Client(tx, d).OrderItem.Query().Count(tx)
				ready.Done()
				if err != nil {
					return err
				}
				<-start
				_, err = uc.CreateOrder(tx, CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1}}})
				return err
			})
			if err == nil {
				succeeded.Add(1)
			} else {
				t.Log(err)
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	if succeeded.Load() != 1 || d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatalf("quota oversold or no winner: %d", succeeded.Load())
	}
}
