package coupon

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	couponent "github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
)

func TestCouponMarkUsedClaimsOnceAndReturnPreservesNewOrder(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	c := d.Client.Coupon.Create().SetName("Single use").SetCode("ONCE").SetType("fixed").SetValue(100).SaveX(ctx)
	if err := r.MarkUsed(ctx, c.ID, 1); err != nil {
		t.Fatal(err)
	}
	for _, oid := range []uint64{1, 2} {
		if err := r.MarkUsed(ctx, c.ID, oid); err == nil {
			t.Fatalf("used coupon accepted order %d", oid)
		}
	}
	if got := d.Client.Coupon.GetX(ctx, c.ID); got.UsedOrderID != 1 {
		t.Fatal("failed claim replaced the winning order")
	}
	if err := r.ReturnByOrder(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkUsed(ctx, c.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.ReturnByOrder(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Coupon.GetX(ctx, c.ID); got.Status != couponent.StatusUsed || got.UsedOrderID != 2 {
		t.Fatal("old return revoked the coupon used by a newer order")
	}
}

func TestCouponMarkUsedRejectsDisabledExpiredAndChangedExpiry(t *testing.T) {
	for _, state := range []string{"disabled", "expired", "expired_after_quote"} {
		t.Run(state, func(t *testing.T) {
			r, d := newMarketingData(t)
			ctx := context.Background()
			c := d.Client.Coupon.Create().SetName("Validity").SetCode("VALIDITY").SetType("fixed").SetValue(100).SetExpireAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
			if state == "expired_after_quote" {
				if _, _, err := r.ResolveScoped(ctx, c.Code, 1, 0, []port.CartItem{{ProductID: 1, Quantity: 1, UnitPrice: 1000}}); err != nil {
					t.Fatal(err)
				}
			}
			if state == "disabled" {
				d.Client.Coupon.UpdateOneID(c.ID).SetStatus(couponent.StatusDisabled).ExecX(ctx)
			} else {
				d.Client.Coupon.UpdateOneID(c.ID).SetExpireAt(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
			}
			if err := r.MarkUsed(ctx, c.ID, 1); err == nil {
				t.Fatal("unavailable coupon accepted a claim")
			}
			if got := d.Client.Coupon.GetX(ctx, c.ID); got.UsedOrderID != 0 || !got.UsedAt.IsZero() {
				t.Fatal("rejected claim left a usage record")
			}
		})
	}
}

func TestCouponUsageAndReturnJoinTransaction(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	c := d.Client.Coupon.Create().SetName("Rollback").SetCode("ROLLBACK").SetType("fixed").SetValue(100).SaveX(ctx)
	rollback := errors.New("abort order")
	err := data.Tx(ctx, d, func(ctx context.Context) error {
		if err := r.MarkUsed(ctx, c.ID, 1); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) || d.Client.Coupon.GetX(ctx, c.ID).Status != couponent.StatusUnused {
		t.Fatal("aborted order consumed its coupon")
	}
	if err := r.MarkUsed(ctx, c.ID, 1); err != nil {
		t.Fatal(err)
	}
	err = data.Tx(ctx, d, func(ctx context.Context) error {
		if err := r.ReturnByOrder(ctx, 1); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) || d.Client.Coupon.GetX(ctx, c.ID).Status != couponent.StatusUsed {
		t.Fatal("aborted refund returned its coupon")
	}
}

func TestMyCouponsExcludeExpiredBeforePagination(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	valid := d.Client.Coupon.Create().SetName("No expiry").SetCode("MY-VALID").SetType("fixed").SetValue(100).SetUserID(1).SaveX(ctx)
	for i := 0; i < 101; i++ {
		d.Client.Coupon.Create().SetName("Expired").SetCode(fmt.Sprintf("MY-EXPIRED-%d", i)).SetType("fixed").SetValue(100).SetUserID(1).SetExpireAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	}
	d.Client.Coupon.Create().SetName("Other owner").SetCode("MY-OTHER").SetType("fixed").SetValue(100).SetUserID(2).SaveX(ctx)
	for _, state := range []couponent.Status{couponent.StatusUsed, couponent.StatusDisabled} {
		d.Client.Coupon.Create().SetName("Unavailable").SetCode("MY-" + string(state)).SetType("fixed").SetValue(100).SetUserID(1).SetStatus(state).SaveX(ctx)
	}
	active := d.Client.Coupon.Create().SetName("Future expiry").SetCode("MY-ACTIVE").SetType("fixed").SetValue(100).SetUserID(1).SetExpireAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	rows, err := r.ListMyCoupons(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != active.ID || rows[1].ID != valid.ID {
		t.Fatalf("available list returned %d coupons instead of both usable coupons", len(rows))
	}
}
