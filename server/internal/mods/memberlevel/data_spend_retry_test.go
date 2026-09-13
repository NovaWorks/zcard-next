package memberlevel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

type failAfterCredit struct {
	delegate walletport.Points
	fail     int
}

func (w *failAfterCredit) PointCreditInTx(ctx context.Context, e walletport.PointEntry) error {
	if err := w.delegate.PointCreditInTx(ctx, e); err != nil {
		return err
	}
	if w.fail > 0 {
		w.fail--
		return errors.New("temporary failure after ledger write")
	}
	return nil
}
func TestPointsFailureRollsBackAndRetries(t *testing.T) {
	d, r, _, w := newMemberLevelEnv(t)
	ctx := context.Background()
	_, err := r.CreateLevel(ctx, "青铜", "consume", 0, 10000, 0, 1, true, map[string]any{"spend_cents": 100, "points": 1})
	if err != nil {
		t.Fatal(err)
	}
	o := d.Client.Order.Create().SetOrderNo("paid-retry").SetUserID(9).SetStatus("completed").SetTotalAmount(10000).SaveX(ctx)
	credit := &failAfterCredit{delegate: walletPorts{repo: w}, fail: 3}
	s := NewPointsService(r, credit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dp := data.NewDispatcher(d, nil)
	dp.Register(data.HandlerReg{Consumer: "memberlevel.points_earn", Type: events.OrderPaid, Fn: s.OnOrderPaid, Transactional: true})
	env := events.Envelope{EventID: 1, Type: events.OrderPaid, Payload: []byte(fmt.Sprintf(`{"order_id":%d,"user_id":9,"total_cents":10000}`, o.ID))}
	if err = dp.Dispatch(ctx, env); err == nil {
		t.Fatal("credit failure acknowledged")
	}
	if d.Client.ProcessedEvent.Query().CountX(ctx) != 0 || d.Client.PointTransaction.Query().CountX(ctx) != 0 || d.Client.PointAccount.Query().CountX(ctx) != 0 {
		t.Fatal("failed transaction left a marker/account/ledger")
	}
	for i := 0; i < 3; i++ {
		if err = dp.Dispatch(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	balance, err := w.GetPoints(ctx, 9)
	if err != nil || balance != 100 || d.Client.PointTransaction.Query().CountX(ctx) != 1 || d.Client.ProcessedEvent.Query().CountX(ctx) != 1 {
		t.Fatal("retry/duplicate", balance, err)
	}
	env.EventID = 2 // A separately emitted event for the same order must not credit twice.
	if err = dp.Dispatch(ctx, env); err != nil {
		t.Fatal(err)
	}
	balance, _ = w.GetPoints(ctx, 9)
	if balance != 100 {
		t.Fatal("order credited twice")
	}
}
func TestSpendingStatusesAndBronzeThreshold(t *testing.T) {
	d, r, s, w := newMemberLevelEnv(t)
	ctx := context.Background()
	_, err := r.CreateLevel(ctx, "青铜", "consume", 0, 10000, 0, 1, true, map[string]any{"spend_cents": 100, "points": 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"paid", "completed", "canceled", "expired", "pending_payment", "refunded"} {
		o := d.Client.Order.Create().SetOrderNo(fmt.Sprint("status-", i)).SetUserID(9).SetStatus(order.Status(status)).SetTotalAmount(1000).SaveX(ctx)
		if status == "paid" || status == "completed" {
			if err = s.OnOrderPaid(ctx, events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"user_id":9,"total_cents":1000}`, o.ID))}); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, err := r.ResolveProgress(ctx, 9)
	if err != nil || p.ConsumedCents != 2000 || p.Current != nil {
		t.Fatal(p, err)
	}
	balance, _ := w.GetPoints(ctx, 9)
	if balance != 0 {
		t.Fatal("below-threshold buyer received points")
	}
	totals, err := data.UserSpending(ctx, d.Client, []uint64{9, 0, 10})
	if err != nil || totals[9] != p.ConsumedCents || totals[0] != 0 || totals[10] != 0 {
		t.Fatal(totals, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = data.UserSpending(canceled, d.Client, []uint64{9}); err == nil {
		t.Fatal("query error concealed as zero")
	}
}

func TestPointsTransientFailureAutomaticallyRetries(t *testing.T) {
	d, r, _, w := newMemberLevelEnv(t)
	ctx := context.Background()
	_, err := r.CreateLevel(ctx, "基础会员", "consume", 0, 0, 0, 1, true, map[string]any{"spend_cents": 100, "points": 1})
	if err != nil {
		t.Fatal(err)
	}
	credit := &failAfterCredit{delegate: walletPorts{repo: w}, fail: 1}
	s := NewPointsService(r, credit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dp := data.NewDispatcher(d, nil)
	dp.Register(data.HandlerReg{Consumer: "memberlevel.points_earn", Type: events.OrderPaid, Fn: s.OnOrderPaid, Transactional: true})
	if err = dp.Dispatch(ctx, events.Envelope{EventID: 1, Type: events.OrderPaid, Payload: []byte(`{"order_id":1,"user_id":9,"total_cents":1000}`)}); err != nil {
		t.Fatal(err)
	}
	pts, err := w.GetPoints(ctx, 9)
	if err != nil || pts != 10 || d.Client.PointTransaction.Query().CountX(ctx) != 1 {
		t.Fatal(pts, err)
	}
}
