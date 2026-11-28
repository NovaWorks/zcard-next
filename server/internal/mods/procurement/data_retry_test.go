package procurement

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

type retryRecordingQueue struct{ tasks []queue.Task }

func (q *retryRecordingQueue) Enabled() bool { return true }
func (q *retryRecordingQueue) Enqueue(_ context.Context, task queue.Task) error {
	q.tasks = append(q.tasks, task)
	return nil
}

func TestSafeSubmissionRetryReusesOriginalRequest(t *testing.T) {
	s, d, payload, iid := submitFixture(t)
	ctx := context.Background()
	q := &retryRecordingQueue{}
	s.enq = q
	g := &submitGateway{}
	var key string
	g.submit = func(_ context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
		if key == "" {
			key = req.DownstreamOrderNo
		} else if key != req.DownstreamOrderNo {
			t.Fatal("retry changed original request key")
		}
		if req.ConnectionID != 1 || req.ProductCode != "P1" || req.Quantity != 2 {
			t.Fatalf("changed frozen purchase: %+v", req)
		}
		if g.submitCalls == 1 {
			return nil, fmt.Errorf("%w: temporary precheck failure", supplyport.ErrUpstreamRetryable)
		}
		return &supplyport.PurchaseResult{Status: "delivered", UpstreamOrderID: "ORIGINAL", Cards: []string{"A", "B"}}, nil
	}
	s.gw = g
	if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
		t.Fatal(err)
	}
	po, _ := s.repo.GetByOrderItem(ctx, iid)
	if !safeSubmitRetry(po) || po.RetryCount != 1 || len(q.tasks) != 1 || q.tasks[0].ProcessIn != 30*time.Second {
		t.Fatal("retry was not persisted and delayed", po)
	}
	if err := s.PollOne(ctx, po.ID); err != nil {
		t.Fatal(err)
	}
	if g.submitCalls != 1 {
		t.Fatal("submitted before due time")
	}
	d.Client.ProcurementOrder.UpdateOneID(po.ID).SetNextRetryAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
	for range 2 {
		if err := s.PollOne(ctx, po.ID); err != nil {
			t.Fatal(err)
		}
	}
	po, _ = s.repo.Get(ctx, po.ID)
	if po.Status != "fulfilled" || g.submitCalls != 2 || d.Client.OrderDelivery.Query().CountX(ctx) != 2 {
		t.Fatal("retry did not deliver exactly once", po, g.submitCalls)
	}
}

func TestSubmissionRetryProofConsumedBeforeRemoteCall(t *testing.T) {
	s, d, payload, iid := submitFixture(t)
	ctx := context.Background()
	s.gw = &fakeGW{submitErr: supplyport.ErrUpstreamRetryable}
	if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
		t.Fatal(err)
	}
	po, _ := s.repo.GetByOrderItem(ctx, iid)
	d.Client.ProcurementOrder.UpdateOneID(po.ID).SetNextRetryAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
	po, _ = s.repo.Get(ctx, po.ID)
	_, claimed, err := s.repo.claimSubmissionRetry(ctx, po)
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	_, claimed, err = s.repo.claimSubmissionRetry(ctx, po)
	if err != nil || claimed {
		t.Fatal("duplicate retry consumed the same proof", err)
	}
	fresh, _ := s.repo.Get(ctx, po.ID)
	if safeSubmitRetry(fresh) {
		t.Fatal("crash could replay an in-flight purchase")
	}
}

func TestSubmissionRetryStopsForClosedOrUncertainOrder(t *testing.T) {
	for _, mode := range []string{"closed", "uncertain", "generic"} {
		t.Run(mode, func(t *testing.T) {
			s, d, payload, iid := submitFixture(t)
			ctx := context.Background()
			err := supplyport.ErrUpstreamRetryable
			if mode == "uncertain" {
				err = supplyport.ErrUpstreamUncertain
			}
			if mode == "generic" {
				err = errors.New("unknown failure")
			}
			g := &fakeGW{submitErr: err}
			s.gw = g
			if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
				t.Fatal(err)
			}
			po, _ := s.repo.GetByOrderItem(ctx, iid)
			if mode == "closed" {
				d.Client.Order.UpdateOneID(payload.OrderID).SetStatus(order.StatusCanceled).ExecX(ctx)
			}
			d.Client.ProcurementOrder.UpdateOneID(po.ID).SetNextRetryAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
			if err := s.PollOne(ctx, po.ID); err != nil {
				t.Fatal(err)
			}
			po, _ = s.repo.Get(ctx, po.ID)
			if po.Status != "manual" || g.submitCalls != 1 {
				t.Fatal("unsafe order resubmitted", po, g.submitCalls)
			}
		})
	}
}

func TestPollSuccessorHasNewGenerationAndDelay(t *testing.T) {
	s, _, _, iid := submitFixture(t)
	ctx := context.Background()
	q := &retryRecordingQueue{}
	s.enq = q
	s.gw = &fakeGW{queryRes: &supplyport.PurchaseOrderInfo{Status: "pending"}}
	po, err := s.repo.CreatePending(ctx, iid, 1, "P1", 2, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MarkSubmitted(ctx, po.ID, "UP", time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.PollOne(ctx, po.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.tasks) != 2 || q.tasks[0].DedupeKey == q.tasks[1].DedupeKey || q.tasks[0].ProcessIn != 30*time.Second || q.tasks[1].ProcessIn != 30*time.Second {
		t.Fatal("poll successor collides with active task or loses delay", q.tasks)
	}
}

func TestPollScannerSkipsFutureDueRows(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	_, iid := seedOrderItem(t, d)
	var dueID uint64
	for i := 0; i < 101; i++ {
		po, err := repo.CreatePending(ctx, iid+uint64(i*100), 1, "P1", 1, "manual", "")
		if err != nil {
			t.Fatal(err)
		}
		if i < 100 {
			if err := repo.MarkSubmitted(ctx, po.ID, fmt.Sprint(i), time.Now().UTC().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		} else {
			dueID = po.ID
			d.Client.ProcurementOrder.UpdateOneID(po.ID).SetNextRetryAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
		}
	}
	rows, err := repo.ListPollable(ctx, 100)
	if err != nil || len(rows) != 1 || rows[0].ID != dueID {
		t.Fatalf("due pending order was starved: count=%d err=%v", len(rows), err)
	}
}
