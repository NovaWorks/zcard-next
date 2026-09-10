package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
)

type releaseFailure struct{ fakeInventory }

func (releaseFailure) Release(context.Context, uint64) error { return errors.New("release failed") }

type slowFailure struct{}

func (slowFailure) HasPendingSlowPayment(context.Context, uint64) (bool, error) {
	return false, errors.New("query failed")
}

func TestCancelRollbackAndIdempotency(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("rollback").SetExpiredAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	uc.Inv = releaseFailure{}
	if err := uc.CancelOrder(ctx, o.OrderNo, "test", "system", 0); err == nil {
		t.Fatal("release failure hidden")
	}
	got := d.Client.Order.GetX(ctx, o.ID)
	if got.Status != order.StatusPendingPayment || !got.ClosedAt.IsZero() {
		t.Fatal("partial cancellation committed")
	}
	if d.Client.OrderStatusEvent.Query().Where(orderstatusevent.OrderID(o.ID)).CountX(ctx) != 0 {
		t.Fatal("event did not roll back")
	}
	uc.Inv = fakeInventory{}
	for i := 0; i < 2; i++ {
		if err := uc.CancelOrder(ctx, o.OrderNo, "test", "system", 0); err != nil {
			t.Fatal(err)
		}
	}
	if d.Client.OrderStatusEvent.Query().Where(orderstatusevent.OrderID(o.ID)).CountX(ctx) != 1 {
		t.Fatal("duplicate cancel event")
	}
}

func TestExpiryFailuresHaveBoundedReview(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour)
	o := d.Client.Order.Create().SetOrderNo("query-error").SetExpiredAt(past).SaveX(ctx)
	uc.SetSlowPaymentChecker(slowFailure{})
	for i := 0; i < 3; i++ {
		if _, err := uc.ExpireOrder(ctx); err == nil {
			t.Fatal("scan hid payment error")
		}
		got := d.Client.Order.GetX(ctx, o.ID)
		if !got.ExpiredAt.Equal(past) || got.ExpiryAttempts != int32(i+1) {
			t.Fatal("deadline changed or attempt lost")
		}
		d.Client.Order.UpdateOneID(o.ID).SetExpiryRetryAt(past).SaveX(ctx)
	}
	got := d.Client.Order.GetX(ctx, o.ID)
	if !got.ExpiryReview || got.Status != order.StatusPendingPayment {
		t.Fatal("uncertain payment must need review")
	}
	if n, err := uc.ExpireOrder(ctx); n != 0 || err != nil {
		t.Fatal("review order retried automatically")
	}
}

func TestCancelUsesVersionGuard(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("cas").SaveX(ctx)
	// Inject a competing state change in the transaction immediately before the
	// cancellation UPDATE. A missing CAS would overwrite it and release stock.
	injected := false
	d.Client.Order.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			om, ok := m.(*ent.OrderMutation)
			if ok && !injected {
				if st, ok := om.Status(); ok && st == order.StatusCanceled {
					injected = true
					om.Where(order.VersionEQ(999))
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	if err := uc.CancelOrder(ctx, o.OrderNo, "test", "system", 0); err == nil {
		t.Fatal("zero affected rows accepted")
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusPendingPayment {
		t.Fatal("conflict committed")
	}
}

type scopeSettings map[string]string

func (s scopeSettings) GetJSON(_ context.Context, _, key string) ([]byte, error) {
	return []byte(s[key]), nil
}
func TestAllBuyerContactScope(t *testing.T) {
	uc := &OrderUsecase{Settings: scopeSettings{"contact_scope": "\"all\"", "contact_required": "\"any\""}}
	for _, uid := range []uint64{0, 9} {
		for _, contact := range []string{"", "   ", "invalid"} {
			if err := uc.validateTradeRequirements(context.Background(), CreateOrderInput{UserID: uid, QueryPassword: "abcd", Contact: contact}); err == nil {
				t.Fatalf("accepted %q uid %d", contact, uid)
			}
		}
		if err := uc.validateTradeRequirements(context.Background(), CreateOrderInput{UserID: uid, QueryPassword: "abcd", Contact: "buyer@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExpiryPagesPast500AndReviewsMissingDeadline(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	missing := d.Client.Order.Create().SetOrderNo("missing-deadline").SaveX(ctx)
	for i := 0; i < 501; i++ {
		d.Client.Order.Create().SetOrderNo(fmt.Sprintf("batch-%d", i)).SetExpiredAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	}
	n, err := uc.ExpireOrder(ctx)
	if err != nil || n != 501 {
		t.Fatalf("processed %d: %v", n, err)
	}
	got := d.Client.Order.GetX(ctx, missing.ID)
	if !got.ExpiryReview || got.Status != order.StatusPendingPayment {
		t.Fatal("missing deadline silently skipped or canceled")
	}
}

func TestSlowPaymentGraceAndLegacyReviewRecovery(t *testing.T) {
	d, uc, repo := newIdemEnv(t)
	uc.SetSlowPaymentChecker(repo)
	ctx := context.Background()
	now := time.Now().UTC()
	seed := func(no string, deadline time.Time, review bool, reason string) *ent.Order {
		o := d.Client.Order.Create().SetOrderNo(no).SetExpiredAt(deadline).SetExpiryReview(review).SetExpiryReason(reason).SaveX(ctx)
		// A recent/retried payment attempt must not extend the order's original deadline.
		d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("usdt").SetDriverSnapshot("epusdt").SetAmount(100).SetExpiresAt(now.Add(time.Hour)).SaveX(ctx)
		return o
	}
	waiting := seed("grace-wait", now.Add(-time.Minute), false, "")
	nearEnd := seed("grace-near-end", now.Add(-14*time.Minute), false, "")
	expired := seed("grace-expired", now.Add(-16*time.Minute), false, "")
	oldReview := seed("legacy-pending-review", now.Add(-8*time.Hour), true, legacySlowPaymentReviewReason)
	manual := seed("real-query-review", now.Add(-8*time.Hour), true, "支付状态查询失败，请核对支付流水")
	paid := seed("already-paid", now.Add(-8*time.Hour), false, "")
	d.Client.Order.UpdateOneID(paid.ID).SetStatus(order.StatusPaid).SaveX(ctx)
	n, err := uc.ExpireOrder(ctx)
	if err != nil || n != 2 {
		t.Fatalf("canceled=%d: %v", n, err)
	}
	if got := d.Client.Order.GetX(ctx, nearEnd.ID); !got.ExpiryRetryAt.Equal(nearEnd.ExpiredAt.Add(15 * time.Minute)) {
		t.Fatal("retry extended the grace deadline")
	}
	for _, o := range []*ent.Order{expired, oldReview} {
		got := d.Client.Order.GetX(ctx, o.ID)
		if got.Status != order.StatusCanceled || got.ExpiryReview || got.ClosedAt.IsZero() || !got.ExpiredAt.Equal(o.ExpiredAt) {
			t.Fatalf("not canceled cleanly: %+v", got)
		}
	}
	// More than three ordinary waits never put the order in permanent review.
	for i := 0; i < 3; i++ {
		d.Client.Order.UpdateOneID(waiting.ID).SetExpiryRetryAt(now.Add(-time.Minute)).SaveX(ctx)
		if n, err = uc.ExpireOrder(ctx); err != nil || n != 0 {
			t.Fatalf("grace scan %d: %v", n, err)
		}
	}
	got := d.Client.Order.GetX(ctx, waiting.ID)
	if got.Status != order.StatusPendingPayment || got.ExpiryReview || got.ExpiryAttempts != 4 {
		t.Fatalf("bounded wait became manual review: %+v", got)
	}
	d.Client.Order.UpdateOneID(waiting.ID).SetExpiredAt(now.Add(-16 * time.Minute)).SetExpiryRetryAt(now.Add(-time.Minute)).SaveX(ctx)
	if n, err = uc.ExpireOrder(ctx); err != nil || n != 1 {
		t.Fatalf("grace end canceled=%d: %v", n, err)
	}
	if n, err = uc.ExpireOrder(ctx); err != nil || n != 0 {
		t.Fatalf("duplicate expiry=%d: %v", n, err)
	}
	if d.Client.Order.GetX(ctx, manual.ID).Status != order.StatusPendingPayment || !d.Client.Order.GetX(ctx, manual.ID).ExpiryReview {
		t.Fatal("query error review was automatically canceled")
	}
	if d.Client.Order.GetX(ctx, paid.ID).Status != order.StatusPaid {
		t.Fatal("paid order was canceled")
	}
	// Normal confirmation waits must not exhaust retries for a new query error.
	waitError := seed("wait-then-error", now.Add(-time.Minute), false, slowPaymentWaitingReason)
	d.Client.Order.UpdateOneID(waitError.ID).SetExpiryAttempts(4).SaveX(ctx)
	uc.SetSlowPaymentChecker(slowFailure{})
	if _, err := uc.ExpireOrder(ctx); err == nil {
		t.Fatal("query failure was hidden")
	}
	if got := d.Client.Order.GetX(ctx, waitError.ID); got.ExpiryAttempts != 1 || got.ExpiryReview {
		t.Fatal("normal waits exhausted query-error retries")
	}
}
