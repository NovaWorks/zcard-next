package order

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	memberport "github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
	pluginport "github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

type pausedRecharge struct {
	inner           walletport.RechargeReader
	once            sync.Once
	reached, resume chan struct{}
}

func (r *pausedRecharge) CumulativeRecharge(ctx context.Context, u uint64) (int64, error) {
	r.once.Do(func() {
		close(r.reached)
		select {
		case <-r.resume:
		case <-ctx.Done():
		}
	})
	return r.inner.CumulativeRecharge(ctx, u)
}

type pausedMembership struct {
	inner           memberport.RateResolver
	once            sync.Once
	reached, resume chan struct{}
}

func (r *pausedMembership) EffectiveRate(ctx context.Context, u uint64) (int32, uint64, error) {
	r.once.Do(func() {
		close(r.reached)
		select {
		case <-r.resume:
		case <-ctx.Done():
		}
	})
	return r.inner.EffectiveRate(ctx, u)
}
func p2Wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent transaction did not reach barrier")
	}
}

// A real resolver is paused between its level and wallet SQL reads. The writer
// changes BOTH assignment and recharge; all reads, pricing and coupon scope must
// still agree on one committed snapshot (SQLite serializes the writer).
func p2MemberSnapshot(t *testing.T, e *p2Env, driver string) {
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	uc := *e.uc
	r := &pausedRecharge{inner: wallet.NewWalletRepoImpl(e.d), reached: make(chan struct{}), resume: make(chan struct{})}
	counter := &countedMembership{inner: memberlevel.NewMemberLevelRepoImpl(e.d, r)}
	uc.MemberRate = counter
	uc.Coupon = coupon.NewCouponRepoImpl(e.d)
	e.d.Client.Coupon.Create().SetName("P2 snapshot").SetCode("p2-level-coupon").SetType("fixed").SetValue(50).SetScope(map[string]any{"level_ids": []uint64{e.auto.ID}}).SaveX(ctx)
	in := e.input(e.automatic.ID)
	in.CouponCode = "p2-level-coupon"
	type outcome struct {
		out *CreateOrderResult
		err error
	}
	done := make(chan outcome, 1)
	go func() { o, err := uc.CreateOrder(ctx, in); done <- outcome{o, err} }()
	p2Wait(t, r.reached)
	written := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		written <- data.Tx(ctx, e.d, func(txCtx context.Context) error {
			c := data.Client(txCtx, e.d)
			denied := e.d.Client.User.GetX(ctx, e.denied.ID).ManualLevelID
			if err := c.User.UpdateOneID(e.automatic.ID).SetManualLevelID(denied).Exec(txCtx); err != nil {
				return err
			}
			_, err := c.WalletTransaction.Update().Where(wallettransaction.UserID(e.automatic.ID)).SetAmount(0).Save(txCtx)
			return err
		})
	}()
	p2Wait(t, started)
	if driver != "sqlite" {
		if err := <-written; err != nil {
			t.Fatal(err)
		}
	} else {
		select {
		case err := <-written:
			t.Fatalf("SQLite writer escaped purchase lock: %v", err)
		case <-time.After(40 * time.Millisecond):
		}
	}
	close(r.resume)
	result := <-done
	if result.err != nil || result.out.TotalCents != 800 || counter.calls.Load() != 1 {
		t.Fatalf("mixed member snapshot: %+v %v calls=%d", result.out, result.err, counter.calls.Load())
	}
	if driver == "sqlite" {
		if err := <-written; err != nil {
			t.Fatal(err)
		}
	}
	before := p2Snapshot(t, e.d)
	if _, err := e.uc.CreateOrder(ctx, e.input(e.automatic.ID)); err == nil {
		t.Fatal("next purchase ignored new assignment")
	}
	if before != p2Snapshot(t, e.d) {
		t.Fatal("denied next purchase changed data")
	}
	e.d.Client.User.UpdateOneID(e.automatic.ID).SetManualLevelID(0).ExecX(ctx)
	e.d.Client.WalletTransaction.Update().Where(wallettransaction.UserID(e.automatic.ID)).SetAmount(500).ExecX(ctx)
}

// First save starts after purchase locked an as-yet-unrestricted product. The
// purchase finishes on its old snapshot, then all subsequent orders see the rule.
func p2FirstRuleRace(t *testing.T, e *p2Env) {
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	p := e.d.Client.Product.Create().SetName("first-rule").SetSlug("p2-first-rule").SetPrice(100).SetStatus(1).SaveX(ctx)
	uc := *e.uc
	uc.Inv = fakeInventory{}
	r := &pausedMembership{inner: uc.MemberRate, reached: make(chan struct{}), resume: make(chan struct{})}
	uc.MemberRate = r
	in := e.input(e.denied.ID)
	in.Items = []OrderItemInput{{ProductID: p.ID, Quantity: 1}}
	purchased := make(chan error, 1)
	go func() { _, err := uc.CreateOrder(ctx, in); purchased <- err }()
	p2Wait(t, r.reached)
	started := make(chan struct{})
	saved := make(chan error, 1)
	go func() {
		close(started)
		_, err := e.repo.Save(ctx, pluginport.SaveConfig{Key: pluginport.RuleKey{PluginID: e.command.PluginID, ProductID: p.ID}, Actor: e.command.Actor, Expected: pluginport.Expected{Generation: 2, SchemaVersion: 1}, Config: pc.Config{SchemaVersion: 1, Revision: "0", Enabled: true, AllowedLevelIDs: []pc.Decimal{pc.Decimal(fmt.Sprint(e.level.ID))}}})
		saved <- err
	}()
	p2Wait(t, started)
	select {
	case err := <-saved:
		t.Fatalf("rule save passed locked product: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(r.resume)
	if err := <-purchased; err != nil {
		t.Fatal("prior snapshot purchase failed", err)
	}
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	before := p2Snapshot(t, e.d)
	if _, err := uc.CreateOrder(ctx, in); err == nil {
		t.Fatal("first committed rule not enforced")
	}
	if before != p2Snapshot(t, e.d) {
		t.Fatal("rule denial changed business data")
	}
}

func p2ConcurrentIdempotency(t *testing.T, e *p2Env) {
	in := e.input(e.allowed.ID)
	in.IdempotencyKey = "p2-concurrent"
	type outcome struct {
		o *CreateOrderResult
		e error
	}
	done := make(chan outcome, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; o, err := e.uc.CreateOrder(e.ctx, in); done <- outcome{o, err} }()
	}
	close(start)
	a, b := <-done, <-done
	if a.e != nil || b.e != nil || a.o.OrderNo != b.o.OrderNo {
		t.Fatalf("concurrent replay: %+v %+v", a, b)
	}
	// A foreign caller has its own namespace and must not receive the original.
	foreign := in
	foreign.UserID = e.denied.ID
	if o, err := e.uc.CreateOrder(e.ctx, foreign); err == nil || o != nil {
		t.Fatal("foreign caller received another user's replay")
	}
}

func p2OppositeCartLocks(t *testing.T, e *p2Env) {
	p := e.d.Client.Product.Create().SetName("second cart item").SetSlug("p2-second-cart").SetPrice(100).SetStatus(1).SaveX(e.ctx)
	uc := *e.uc
	uc.Inv = fakeInventory{} // This test isolates lock ordering from stock contention.
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, items := range [][]OrderItemInput{
		{{ProductID: e.product.ID, Quantity: 1}, {ProductID: p.ID, Quantity: 1}},
		{{ProductID: p.ID, Quantity: 1}, {ProductID: e.product.ID, Quantity: 1}},
	} {
		go func(items []OrderItemInput) {
			<-start
			in := e.input(e.allowed.ID)
			in.Items = items
			out, err := uc.CreateOrder(ctx, in)
			if err == nil && out.TotalCents != 990 {
				err = fmt.Errorf("unexpected cart price: %d", out.TotalCents)
			}
			done <- err
		}(items)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("opposite cart lock order failed", err)
		}
	}
}
