//go:build integration

package testint

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	couponent "github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	couponport "github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// Different products avoid product-lock serialization and force both orders to
// resolve the same unused coupon before either transaction attempts to claim it.
type couponResolveBarrier struct {
	*coupon.CouponRepoImpl
	arrivals atomic.Int32
	release  chan struct{}
}

func (b *couponResolveBarrier) ResolveScoped(ctx context.Context, code string, user, level uint64, items []couponport.CartItem) (money.Cents, uint64, error) {
	value, cid, err := b.CouponRepoImpl.ResolveScoped(ctx, code, user, level, items)
	if err != nil {
		return value, cid, err
	}
	if b.arrivals.Add(1) == 2 {
		close(b.release)
	}
	select {
	case <-b.release:
		return value, cid, nil
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}
}

func TestCouponConcurrentPhysicalMySQL(t *testing.T) { couponConcurrentPhysical(MySQL(t)) }
func TestCouponConcurrentPhysicalPG(t *testing.T)    { couponConcurrentPhysical(PG(t)) }

func couponConcurrentPhysical(h *Harness) {
	t := h.T
	d := h.Data
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	base := coupon.NewCouponRepoImpl(d)
	uc := &order.OrderUsecase{Data: d, Gen: gen, Inv: inventory.NewCardRepoImpl(d, nil), Coupon: base, Outbox: data.NewOutboxWriter(d)}
	c := d.Client.Coupon.Create().SetName("Single use").SetCode("CONCURRENT-COUPON").SetType("fixed").SetValue(100).SaveX(ctx)
	inputs := []order.CreateOrderInput{}
	pids := []uint64{}
	for i := 0; i < 2; i++ {
		p := d.Client.Product.Create().SetName(fmt.Sprintf("Parcel %d", i)).SetSlug(fmt.Sprintf("coupon-parcel-%d", i)).SetPrice(1000).SetGoodsType("physical").SetPhysicalStock(2).SetShippingMode("fixed").SetShippingFee(300).SetShippingCountries([]string{"US"}).SetStatus(1).SaveX(ctx)
		pids = append(pids, p.ID)
		in := order.CreateOrderInput{Items: []order.OrderItemInput{{ProductID: p.ID, Quantity: 1}}, QueryPassword: "test-query-password", Contact: "test@example.invalid", CouponCode: c.Code, IdempotencyKey: fmt.Sprintf("coupon-cart-%d", i), ShippingAddress: map[string]string{"country": "US", "region": "CA", "city": "SF", "address": "123 Test Street", "name": "Test Buyer", "phone": "+14155550100", "postal_code": "94105"}, QuoteOnly: true}
		q, err := uc.CreateOrder(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		in.QuoteOnly, in.QuoteKey = false, q.QuoteKey
		inputs = append(inputs, in)
	}
	barrier := &couponResolveBarrier{CouponRepoImpl: base, release: make(chan struct{})}
	uc.Coupon = barrier
	var wg sync.WaitGroup
	results := make(chan error, len(inputs))
	for _, in := range inputs {
		wg.Add(1)
		go func(in order.CreateOrderInput) {
			defer wg.Done()
			_, err := uc.CreateOrder(ctx, in)
			results <- err
		}(in)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			t.Logf("competing order rejected: %v", err)
		}
	}
	if barrier.arrivals.Load() != 2 || wins != 1 || d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatalf("coupon claim committed %d orders; both orders reached resolution=%t", wins, barrier.arrivals.Load() == 2)
	}
	o := d.Client.Order.Query().OnlyX(ctx)
	used := d.Client.Coupon.GetX(ctx, c.ID)
	stock := d.Client.Product.GetX(ctx, pids[0]).PhysicalStock + d.Client.Product.GetX(ctx, pids[1]).PhysicalStock
	if used.Status != couponent.StatusUsed || used.UsedOrderID != o.ID || stock != 3 || o.TotalAmount != 1200 || o.ShippingAmount != 300 || d.Client.OrderItem.Query().CountX(ctx) != 1 || d.Client.PhysicalStockMovement.Query().CountX(ctx) != 1 {
		t.Fatal("losing coupon claim retained order rows or inventory changes")
	}
	// An actual lifecycle adapter returns the coupon with a complete unshipped
	// refund, and an idempotent refund replay cannot revoke subsequent reuse.
	uc.Coupon = base
	if err := uc.MarkPaid(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	repo := payment.NewPaymentRepoImpl(d, nil, nil, order.ProvideOrderLifecycle(uc), nil, nil, data.NewOutboxWriter(d), nil, nil, nil)
	zero := int64(0)
	req := &adminv1.CreateRefundRequest{Channel: "gateway", RequestKey: "coupon-full-refund-request", ExternalConfirmed: true, ExternalReference: "TEST-COUPON-REFUND", AmountCents: 1200, ExpectedRefundedCents: &zero, ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":900,"shipping_cents":300,"cancel_quantity":1}]`, it.ID)}
	first, err := repo.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Coupon.GetX(ctx, c.ID); got.Status != couponent.StatusUnused || got.UsedOrderID != 0 {
		t.Fatal("wired lifecycle did not return the coupon after full unshipped refund")
	}
	if err := base.MarkUsed(ctx, c.ID, o.ID+1); err != nil {
		t.Fatal(err)
	}
	if err := base.ReturnByOrder(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := repo.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Coupon.GetX(ctx, c.ID); got.UsedOrderID != o.ID+1 || first.ID != replay.ID {
		t.Fatal("old refund return or replay revoked a newer coupon use")
	}
}
