package procurement

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

// Obtain the real driver error rather than substituting a string containing
// SQLITE_BUSY: a WAL read snapshot cannot upgrade after another writer commits.
func procurementBusySnapshot(t *testing.T) error {
	t.Helper()
	ctx := context.Background()
	h, err := db.SQLite.Open("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "busy.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetMaxOpenConns(2)
	if _, err := h.ExecContext(ctx, "CREATE TABLE fixture (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ExecContext(ctx, "INSERT INTO fixture (id, value) VALUES (1, 0)"); err != nil {
		t.Fatal(err)
	}
	tx, err := h.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before int
	if err := tx.QueryRowContext(ctx, "SELECT value FROM fixture WHERE id=1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ExecContext(ctx, "UPDATE fixture SET value=value+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, "UPDATE fixture SET value=value+1 WHERE id=1")
	var code interface{ Code() int }
	if !errors.As(err, &code) || code.Code() != 517 {
		t.Fatalf("fixture did not produce SQLITE_BUSY_SNAPSHOT (517): %v", err)
	}
	return err
}

// Fail after both procurement rows were written. The second attempt must see
// neither row from the rolled-back attempt, and must not increment version twice.
func failFirstProcurementItemWithBusy(t *testing.T, d *data.Data, orderID uint64, busy error) *int {
	t.Helper()
	attempts := 0
	initialVersion := d.Client.Order.GetX(context.Background(), orderID).Version
	d.Client.ProcurementItem.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if !m.Op().Is(ent.OpCreate) {
				return next.Mutate(ctx, m)
			}
			attempts++
			c := data.Client(ctx, d)
			if attempts > 1 && c.ProcurementItem.Query().CountX(ctx) != 0 {
				t.Fatal("failed claim left its procurement item behind")
			}
			if c.ProcurementOrder.Query().CountX(ctx) != 1 {
				t.Fatal("claim did not contain exactly one procurement order")
			}
			version := c.Order.GetX(ctx, orderID).Version
			if version > initialVersion+1 {
				t.Fatal("failed claim leaked an order version increment")
			}
			v, err := next.Mutate(ctx, m)
			if err == nil && attempts == 1 {
				return nil, fmt.Errorf("after writing procurement item: %w", busy)
			}
			return v, err
		})
	})
	return &attempts
}

func TestClaimBusySnapshotRetrySubmitsOnlyAfterCommit(t *testing.T) {
	busy := procurementBusySnapshot(t)
	for _, mode := range []string{"accepted", "non_json", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			s, d, payload, iid := submitFixture(t)
			ctx := context.Background()
			initialVersion := d.Client.Order.GetX(ctx, payload.OrderID).Version
			attempts := failFirstProcurementItemWithBusy(t, d, payload.OrderID, busy)
			g := &submitGateway{}
			g.submit = func(ctx context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
				if *attempts != 2 {
					t.Fatalf("provider called before retried claim committed: attempts=%d", *attempts)
				}
				// A fresh client (not the transaction client in ctx) proves commit
				// preceded the irreversible remote call.
				po := d.Client.ProcurementOrder.Query().OnlyX(ctx)
				if po.DedupeKey != req.DownstreamOrderNo || d.Client.ProcurementItem.Query().CountX(ctx) != 1 {
					t.Fatal("provider request has no unique committed claim")
				}
				if d.Client.Order.GetX(ctx, payload.OrderID).Version != initialVersion+1 {
					t.Fatal("claim retry did not roll back the first version increment")
				}
				switch mode {
				case "non_json":
					return nil, fmt.Errorf("%w: invalid JSON response after trade", supplyport.ErrUpstreamUncertain)
				case "timeout":
					return nil, fmt.Errorf("%w: %w", supplyport.ErrUpstreamUncertain, context.DeadlineExceeded)
				default:
					return &supplyport.PurchaseResult{Status: "pending", UpstreamOrderID: "BUSY-RECEIPT"}, nil
				}
			}
			queryCalls := 0
			g.query = func(context.Context, uint64, string) (*supplyport.PurchaseOrderInfo, error) {
				queryCalls++
				return &supplyport.PurchaseOrderInfo{Status: "pending"}, nil
			}
			s.gw = g
			if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
				t.Fatal(err)
			}
			po, err := s.repo.GetByOrderItem(ctx, iid)
			if err != nil {
				t.Fatal(err)
			}
			if *attempts != 2 || g.submitCalls != 1 || d.Client.ProcurementOrder.Query().CountX(ctx) != 1 || d.Client.ProcurementItem.Query().CountX(ctx) != 1 {
				t.Fatalf("claim rollback/retry duplicated purchase: attempts=%d submits=%d", *attempts, g.submitCalls)
			}
			key := po.DedupeKey
			if mode != "accepted" && (po.Status != procurementorder.StatusManual || po.UpstreamOrderID != "" || po.LastError == "") {
				t.Fatalf("ambiguous submit was not preserved for review: %+v", po)
			}
			for replay := 0; replay < 2; replay++ {
				if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
					t.Fatal(err)
				}
				if err := s.PollOne(ctx, po.ID); err != nil {
					t.Fatal(err)
				}
				_, retryErr := NewAdminProcurementService(s.repo, s).RetryProcurement(ctx, &adminv1.RetryProcurementRequest{Id: po.ID})
				if (mode == "accepted") != (retryErr == nil) {
					t.Fatalf("unexpected admin retry result: %v", retryErr)
				}
			}
			po = d.Client.ProcurementOrder.GetX(ctx, po.ID)
			if *attempts != 2 || g.submitCalls != 1 || po.DedupeKey != key {
				t.Fatal("event, poll or admin replay submitted another purchase")
			}
			if mode != "accepted" && (queryCalls != 0 || po.Status != procurementorder.StatusManual) {
				t.Fatal("uncertain purchase queried without an upstream ID or left review")
			}
			if d.Client.OrderDelivery.Query().CountX(ctx) != 0 || d.Client.RefundOrder.Query().CountX(ctx) != 0 {
				t.Fatal("claim retry or unknown submit fabricated delivery/refund")
			}
		})
	}
}

func TestRecordMissingBusySnapshotRetryRollsBackPlaceholder(t *testing.T) {
	busy := procurementBusySnapshot(t)
	s, d, payload, iid := submitFixture(t)
	ctx := context.Background()
	initialVersion := d.Client.Order.GetX(ctx, payload.OrderID).Version
	attempts := failFirstProcurementItemWithBusy(t, d, payload.OrderID, busy)
	g := &fakeGW{}
	s.gw = g
	if err := s.repo.recordMissing(ctx, iid); err != nil {
		t.Fatal(err)
	}
	po, err := s.repo.GetByOrderItem(ctx, iid)
	if err != nil {
		t.Fatal(err)
	}
	if *attempts != 2 || po.Status != procurementorder.StatusManual || po.LastError != data.MissingProcurementReason {
		t.Fatalf("missing placeholder did not retry to manual: attempts=%d po=%+v", *attempts, po)
	}
	if err := s.repo.recordMissing(ctx, iid); err != nil {
		t.Fatal(err)
	}
	if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
		t.Fatal(err)
	}
	if *attempts != 2 || g.submitCalls != 0 || d.Client.ProcurementOrder.Query().CountX(ctx) != 1 || d.Client.ProcurementItem.Query().CountX(ctx) != 1 {
		t.Fatal("missing-task retry/replay duplicated a placeholder or bought upstream")
	}
	// MarkManual owns one real version change; the no-op lock and failed
	// attempt must not add another one or publish a second status event.
	if d.Client.Order.GetX(ctx, payload.OrderID).Version != initialVersion+1 || d.Client.OrderStatusEvent.Query().CountX(ctx) != 1 {
		t.Fatal("missing-task retry duplicated the manual transition")
	}
}
