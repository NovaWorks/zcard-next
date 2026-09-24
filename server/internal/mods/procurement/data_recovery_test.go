package procurement

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/outboxevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

type receiptQueue struct{}

func (receiptQueue) Enabled() bool                             { return true }
func (receiptQueue) Enqueue(context.Context, queue.Task) error { return nil }

func recoveryFixture(t *testing.T) (*ProcureService, *data.Data, *fulfillment.DeliveryRepoImpl, uint64, uint64, uint64) {
	t.Helper()
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	d.Client.Product.Create().SetID(10).SetName("上游商品").SetSlug("recovery").SetPrice(1000).SaveX(ctx)
	po, err := repo.CreatePending(ctx, iid, 1, "P1", 2, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := newTestCipher(t)
	delivery := fulfillment.NewDeliveryRepoImpl(d, cipher, nil, nil)
	s := &ProcureService{repo: repo, cipher: cipher, attach: delivery, outbox: data.NewOutboxWriter(d), enq: receiptQueue{}, log: slog.Default()}
	return s, d, delivery, oid, iid, po.ID
}

func TestReceiptDeliveryFailureRecovery(t *testing.T) {
	for _, mode := range []string{"sync", "callback", "completion_write", "event_write"} {
		t.Run(mode, func(t *testing.T) {
			s, d, _, oid, iid, pid := recoveryFixture(t)
			ctx := context.Background()
			fail := true
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if fail {
						return nil, errors.New("injected local write failure")
					}
					return next.Mutate(ctx, m)
				})
			}
			switch mode {
			case "completion_write":
				d.Client.ProcurementOrder.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if status, ok := m.(*ent.ProcurementOrderMutation).Status(); fail && ok && status == procurementorder.StatusFulfilled {
							return nil, errors.New("completion unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "event_write":
				d.Client.OutboxEvent.Use(hook)
			default:
				d.Client.OrderDelivery.Use(hook)
			}
			var err error
			if mode == "callback" {
				err = s.confirmResult(ctx, pid, "delivered", []string{"A", "B"}, 100)
			} else {
				err = s.finalizeDelivered(ctx, pid, oid, iid, 10, 0, []string{"A", "B"}, 100)
			}
			if err == nil {
				t.Fatal("delivery failure swallowed")
			}
			if d.Client.ProcurementOrder.GetX(ctx, pid).Status != procurementorder.StatusPolling {
				t.Fatal("failed delivery marked complete")
			}
			if d.Client.OrderDelivery.Query().CountX(ctx) != 0 || d.Client.Card.Query().CountX(ctx) != 0 || d.Client.OutboxEvent.Query().CountX(ctx) != 0 {
				t.Fatal("partial delivery survived rollback")
			}
			saved, err := s.repo.ReceivedContent(ctx, pid)
			if err != nil || len(saved) != 2 {
				t.Fatalf("receipt lost: %v", err)
			}
			fail = false
			// No gateway: success proves recovery cannot make another supplier request.
			if err = s.PollOne(ctx, pid); err != nil {
				t.Fatal(err)
			}
			if d.Client.OrderDelivery.Query().CountX(ctx) != 2 || d.Client.Order.GetX(ctx, oid).Status != order.StatusDelivered || d.Client.ProcurementOrder.GetX(ctx, pid).Status != procurementorder.StatusFulfilled {
				t.Fatal("recovery incomplete")
			}
			if err = s.confirmResult(ctx, pid, "delivered", []string{"DIFFERENT", "CALLBACK"}, 100); err != nil {
				t.Fatal(err)
			}
			if d.Client.OrderDelivery.Query().CountX(ctx) != 2 || d.Client.Card.Query().CountX(ctx) != 2 {
				t.Fatal("duplicate callback delivered again")
			}
			if d.Client.OutboxEvent.Query().Where(outboxevent.TypeEQ(events.ProcurementFulfilled)).CountX(ctx) != 1 {
				t.Fatal("completion event duplicated")
			}
			after, _ := s.repo.ReceivedContent(ctx, pid)
			plain, err := s.cipher.Open(after[0], 10, 0)
			if err != nil || plain != "A" {
				t.Fatal("duplicate callback replaced receipt")
			}
		})
	}
}

func TestHistoricFulfilledRecoveryAndManual(t *testing.T) {
	for _, mode := range []string{"retry", "manual", "refunded", "refund_processing", "partial", "empty"} {
		t.Run(mode, func(t *testing.T) {
			s, d, delivery, oid, iid, pid := recoveryFixture(t)
			ctx := context.Background()
			sealed := [][]byte{}
			for _, v := range []string{"A", "B"} {
				ct, _ := s.cipher.Seal(v, 10, 0)
				sealed = append(sealed, ct)
			}
			if mode != "empty" {
				if err := s.repo.AttachReceivedContent(ctx, pid, sealed); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.repo.MarkFulfilled(ctx, pid); err != nil {
				t.Fatal(err)
			}
			admin := NewAdminProcurementService(s.repo, s)
			switch mode {
			case "refunded":
				d.Client.Order.UpdateOneID(oid).SetStatus(order.StatusRefunded).SaveX(ctx)
			case "refund_processing":
				d.Client.RefundOrder.Create().SetOrderID(oid).SetAmount(1000).SetChannel(refundorder.ChannelUpstream).SetStatus(refundorder.StatusProcessing).SaveX(ctx)
			case "partial":
				d.Client.OrderDelivery.Create().SetOrderID(oid).SetItemID(iid).SetCardID(0).SetDeliveryTokenHash("partial").SetDeliveredMode("status").SetDeliveredAt(time.Now()).SaveX(ctx)
			}
			if mode == "retry" {
				if _, err := admin.RetryProcurement(ctx, &adminv1.RetryProcurementRequest{Id: pid}); err != nil {
					t.Fatal(err)
				}
				if d.Client.OrderDelivery.Query().CountX(ctx) != 2 {
					t.Fatal("old receipt not recovered")
				}
				if err := s.repo.MarkManual(ctx, pid, "manual"); err == nil {
					t.Fatal("delivered item reopened")
				}
				return
			}
			if mode == "refunded" || mode == "refund_processing" {
				if _, err := admin.RetryProcurement(ctx, &adminv1.RetryProcurementRequest{Id: pid}); err == nil {
					t.Fatal("refunding order delivered")
				}
				if err := s.repo.MarkManual(ctx, pid, "manual"); err == nil {
					t.Fatal("refunding order reopened")
				}
				return
			}
			if mode == "partial" || mode == "empty" {
				if err := s.PollOne(ctx, pid); err == nil {
					t.Fatal("invalid receipt silently accepted")
				}
			}
			if err := delivery.ManualDeliver(ctx, "T-ORDER-1", "C\nD", "", "", 1, iid); err == nil {
				t.Fatal("manual guard bypassed")
			}
			if err := s.repo.MarkManual(ctx, pid, "checked upstream"); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.RetryProcurement(ctx, &adminv1.RetryProcurementRequest{Id: pid}); err == nil {
				t.Fatal("manual retry falsely succeeded")
			}
			cards := "C\nD"
			if mode == "partial" {
				cards = "C"
			}
			if err := delivery.ManualDeliver(ctx, "T-ORDER-1", cards, "", "", 1, iid); err != nil {
				t.Fatal(err)
			}
			if d.Client.OrderDelivery.Query().CountX(ctx) != 2 || d.Client.Order.GetX(ctx, oid).Status != order.StatusDelivered {
				t.Fatal("manual completion failed")
			}
		})
	}
}

func TestMissingProcurementAndListLookup(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	d.Client.Order.UpdateOneID(oid).SetPaidAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	if err := repo.reconcileMissing(ctx); err != nil {
		t.Fatal(err)
	}
	po, err := repo.GetByOrderItem(ctx, iid)
	if err != nil || po.Status != procurementorder.StatusManual {
		t.Fatalf("missing order not made visible: %v", err)
	}
	if err := repo.reconcileMissing(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Client.ProcurementOrder.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate missing receipt")
	}
	svc := NewAdminProcurementService(repo, nil)
	for _, filter := range []struct {
		no    string
		total int64
	}{{"T-ORDER-1", 1}, {"OTHER", 0}, {"", 1}} {
		reply, err := svc.ListProcurements(ctx, &adminv1.ListProcurementsRequest{OrderNo: filter.no})
		if err != nil || reply.Total != filter.total {
			t.Fatalf("lookup: %v %v", reply, err)
		}
		if reply.Total > 0 && (!reply.Procurements[0].DeliveryIncomplete || reply.Procurements[0].OrderNo != "T-ORDER-1") {
			t.Fatal("missing customer link/recovery flag")
		}
	}
}

func TestCreatePendingRollsBackIncompleteParent(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	_, iid := seedOrderItem(t, d)
	d.Client.ProcurementItem.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("item unavailable") })
	})
	if _, err := repo.CreatePending(ctx, iid, 1, "P", 2, "manual", ""); err == nil {
		t.Fatal("expected failure")
	}
	if d.Client.ProcurementOrder.Query().CountX(ctx) != 0 {
		t.Fatal("orphaned parent prevents recovery")
	}
}

func TestLateCallbackAfterManualCannotDeliver(t *testing.T) {
	s, d, _, _, _, pid := recoveryFixture(t)
	ctx := context.Background()
	if err := s.repo.MarkManual(ctx, pid, "checked upstream"); err != nil {
		t.Fatal(err)
	}
	if err := s.confirmResult(ctx, pid, "delivered", []string{"A", "B"}, 100); err == nil {
		t.Fatal("late callback ignored manual hold")
	}
	if d.Client.OrderDelivery.Query().CountX(ctx) != 0 {
		t.Fatal("late callback delivered")
	}
	if d.Client.OrderItem.Query().Where(orderitem.FulfillmentStatusEQ("manual")).CountX(ctx) != 1 {
		t.Fatal("manual state lost")
	}
}

type unavailableProductReader struct{ catalogport.ProductReader }

func (unavailableProductReader) Get(context.Context, uint64, uint64) (*catalogport.Product, error) {
	return nil, errors.New("product unavailable")
}
func TestPaymentEventPreparationFailureVisible(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	s := &ProcureService{repo: repo, reader: unavailableProductReader{}, log: slog.Default()}
	raw, _ := json.Marshal(map[string]any{"order_no": "T-ORDER-1", "order_id": oid, "items": []any{map[string]any{"order_item_id": iid, "product_id": 10, "quantity": 2, "fulfillment_type": "upstream"}}})
	if err := s.OnOrderPaid(ctx, events.Envelope{Payload: raw}); err != nil {
		t.Fatal(err)
	}
	po, err := repo.GetByOrderItem(ctx, iid)
	if err != nil || po.Status != procurementorder.StatusManual {
		t.Fatalf("failed preparation invisible: %v", err)
	}
}
func TestPatrolPreservesRecentPaymentAndFindsStalledPending(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	d.Client.Order.UpdateOneID(oid).SetPaidAt(time.Now().UTC()).SaveX(ctx)
	if err := repo.reconcileMissing(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Client.ProcurementOrder.Query().CountX(ctx) != 0 {
		t.Fatal("recent payment prematurely made manual")
	}
	po, err := repo.CreatePending(ctx, iid, 1, "P", 2, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListPollable(ctx, 100)
	if err != nil || len(rows) != 0 {
		t.Fatal("in-flight submission selected")
	}
	if _, err := d.DB.ExecContext(ctx, "UPDATE procurement_orders SET created_at = ? WHERE id = ?", time.Now().UTC().Add(-time.Hour), po.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListPollable(ctx, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal("stalled pending hidden")
	}
	s := &ProcureService{repo: repo, log: slog.Default()}
	if err := s.PollOne(ctx, po.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.ProcurementOrder.GetX(ctx, po.ID).Status != procurementorder.StatusManual {
		t.Fatal("unknown supplier outcome not made manual")
	}
}
