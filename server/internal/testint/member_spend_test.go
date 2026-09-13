//go:build integration

package testint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

type retryPointsPort struct {
	repo     *wallet.WalletRepoImpl
	attempts atomic.Int32
}

func (p *retryPointsPort) PointCreditInTx(ctx context.Context, e walletport.PointEntry) error {
	err := p.repo.PointCreditInTx(ctx, wallet.PointEntry{UserID: e.UserID, Direction: e.Direction, Type: e.Type, Amount: e.Amount, Reference: e.Reference, OrderID: e.OrderID, Remark: e.Remark})
	if err != nil {
		return err
	}
	if p.attempts.Add(1) <= 3 {
		return errors.New("failure after credit")
	}
	return nil
}
func TestMemberSpendingMySQL(t *testing.T) { runMemberSpending(MySQL(t)) }
func TestMemberSpendingPG(t *testing.T)    { runMemberSpending(PG(t)) }
func runMemberSpending(h *Harness) {
	t, ctx, c := h.T, context.Background(), h.Data.Client
	u := c.User.Create().SetUsername("spend-buyer").SaveX(ctx)
	for i := 0; i < 4; i++ {
		c.Order.Create().SetOrderNo(fmt.Sprint("completed-", i)).SetUserID(u.ID).SetStatus("completed").SetTotalAmount(1000).SaveX(ctx)
	}
	c.Order.Create().SetOrderNo("refunded").SetUserID(u.ID).SetStatus("refunded").SetTotalAmount(9000).SaveX(ctx)
	c.Order.Create().SetOrderNo("guest").SetUserID(0).SetStatus("paid").SetTotalAmount(8000).SaveX(ctx)
	totals, err := data.UserSpending(ctx, c, []uint64{u.ID, 0})
	if err != nil || totals[u.ID] != 4000 || totals[0] != 0 {
		t.Fatal(totals, err)
	}
	r := memberlevel.NewMemberLevelRepoImpl(h.Data, nil)
	_, err = r.CreateLevel(ctx, "青铜", "consume", 0, 10000, 0, 1, true, map[string]any{"spend_cents": 100, "points": 1})
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.ResolveProgress(ctx, u.ID)
	if err != nil || before.ConsumedCents != 4000 || before.Current != nil {
		t.Fatal(before, err)
	}
	o := c.Order.Create().SetOrderNo("reach-bronze").SetUserID(u.ID).SetStatus("paid").SetTotalAmount(6000).SaveX(ctx)
	after, err := r.ResolveProgress(ctx, u.ID)
	if err != nil || after.ConsumedCents != 10000 || after.Current == nil {
		t.Fatal(after, err)
	}
	w := wallet.NewWalletRepoImpl(h.Data)
	p := &retryPointsPort{repo: w}
	s := memberlevel.NewPointsService(r, p, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dp := data.NewDispatcher(h.Data, nil)
	dp.Register(data.HandlerReg{Consumer: "memberlevel.points_earn", Type: events.OrderPaid, Fn: s.OnOrderPaid, Transactional: true})
	env := events.Envelope{EventID: 77, Type: events.OrderPaid, Payload: []byte(fmt.Sprintf(`{"order_id":%d,"user_id":%d,"total_cents":6000}`, o.ID, u.ID))}
	if err = dp.Dispatch(ctx, env); err == nil {
		t.Fatal("failed credit was acknowledged")
	}
	if c.ProcessedEvent.Query().CountX(ctx) != 0 || c.PointTransaction.Query().CountX(ctx) != 0 || c.PointAccount.Query().CountX(ctx) != 0 {
		t.Fatal("transaction did not roll back")
	}
	var wg sync.WaitGroup
	errs := make([]error, 12)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = dp.Dispatch(ctx, env) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	pts, err := w.GetPoints(ctx, u.ID)
	if err != nil || pts != 60 || c.PointTransaction.Query().CountX(ctx) != 1 || c.ProcessedEvent.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate credit", pts, err)
	}
}
