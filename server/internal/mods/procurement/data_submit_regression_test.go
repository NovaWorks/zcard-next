package procurement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

type submitProductReader struct{ catalogport.ProductReader }

func (submitProductReader) Get(context.Context, uint64, uint64) (*catalogport.Product, error) {
	return &catalogport.Product{UpstreamSourceID: 1, UpstreamProductCode: "P1"}, nil
}

type submitGateway struct {
	fakeGW
	submit func(context.Context, supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error)
	query  func(context.Context, uint64, string) (*supplyport.PurchaseOrderInfo, error)
}

func (g *submitGateway) Submit(ctx context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
	g.submitCalls++
	return g.submit(ctx, req)
}

func (g *submitGateway) Query(ctx context.Context, id uint64, no string) (*supplyport.PurchaseOrderInfo, error) {
	return g.query(ctx, id, no)
}

func submitFixture(t *testing.T) (*ProcureService, *data.Data, orderPaidPayload, uint64) {
	t.Helper()
	repo, d := newProcureTestData(t)
	oid, iid := seedOrderItem(t, d)
	d.Client.Product.Create().SetID(10).SetName("upstream").SetSlug("submit").SetPrice(1000).SaveX(context.Background())
	cipher := newTestCipher(t)
	s := &ProcureService{repo: repo, reader: submitProductReader{}, cipher: cipher,
		attach: fulfillment.NewDeliveryRepoImpl(d, cipher, nil, nil),
		outbox: data.NewOutboxWriter(d), enq: receiptQueue{}, log: slog.Default()}
	return s, d, orderPaidPayload{OrderNo: "T-ORDER-1", OrderID: oid}, iid
}

// Independent shops can have identical local IDs and order numbers. Model the
// ACG supplier's global request_no uniqueness and CHAR(19) limit.
func TestSubmitKeysAcrossShops(t *testing.T) {
	seen := map[string]bool{}
	var firstItemID uint64
	for shop := 0; shop < 2; shop++ {
		t.Run(fmt.Sprint(shop), func(t *testing.T) {
			s, d, payload, iid := submitFixture(t)
			ctx := context.Background()
			if shop == 0 {
				firstItemID = iid
			} else if iid != firstItemID {
				t.Fatal("test must use the same local item ID in both shops")
			}
			g := &submitGateway{}
			g.submit = func(ctx context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
				po, err := s.repo.GetByOrderItem(ctx, iid)
				if err != nil || po.DedupeKey != req.DownstreamOrderNo {
					t.Fatalf("request key not persisted before purchase: %v", err)
				}
				key := req.DownstreamOrderNo
				if len(key) == 0 || len(key) > 19 {
					t.Fatalf("request_no incompatible with ACG CHAR(19): %q", key)
				}
				if seen[key] {
					return nil, supplyport.ErrUpstreamDuplicate
				}
				seen[key] = true
				return &supplyport.PurchaseResult{Status: "delivered", UpstreamOrderID: "UP-1", Cards: []string{"A", "B"}}, nil
			}
			s.gw = g
			if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
				t.Fatal(err)
			}
			po, err := s.repo.GetByOrderItem(ctx, iid)
			if err != nil || po.Status != procurementorder.StatusFulfilled || d.Client.OrderDelivery.Query().CountX(ctx) != 2 {
				t.Fatalf("shop %d could not auto-deliver: %+v %v", shop, po, err)
			}
			// Event redelivery must reuse the persisted purchase, never buy again.
			if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
				t.Fatal(err)
			}
			if g.submitCalls != 1 {
				t.Fatalf("duplicate payment event purchased %d times", g.submitCalls)
			}
			found, err := NewProcureRepo(d).GetByDownstreamOrderNo(ctx, po.DedupeKey)
			if err != nil || found.ID != po.ID {
				t.Fatalf("persisted key cannot resolve callback: %v", err)
			}
		})
	}
}

func TestSubmitReceiptRecovery(t *testing.T) {
	for _, mode := range []string{"delivered", "cancelled_after_success", "cancelled_after_pending", "receipt_write_failure"} {
		t.Run(mode, func(t *testing.T) {
			s, d, payload, iid := submitFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fail := mode == "receipt_write_failure"
			d.Client.ProcurementItem.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if _, ok := m.(*ent.ProcurementItemMutation).ReceivedContent(); ok && fail {
						return nil, errors.New("receipt storage unavailable")
					}
					return next.Mutate(ctx, m)
				})
			})
			g := &submitGateway{}
			g.submit = func(context.Context, supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
				res := &supplyport.PurchaseResult{Status: "delivered", UpstreamOrderID: "UP-RECEIPT", Cards: []string{"A", "B"}}
				if mode == "cancelled_after_success" || mode == "cancelled_after_pending" {
					cancel()
				}
				if mode == "cancelled_after_pending" {
					res.Status, res.Cards = "pending", nil
				}
				return res, nil
			}
			queries := 0
			g.query = func(_ context.Context, connectionID uint64, no string) (*supplyport.PurchaseOrderInfo, error) {
				queries++
				if connectionID != 1 || no != "UP-RECEIPT" {
					t.Fatalf("queried wrong supplier order: %d %q", connectionID, no)
				}
				return &supplyport.PurchaseOrderInfo{Status: "delivered", Cards: []string{"A", "B"}}, nil
			}
			s.gw = g
			err := s.processItem(ctx, payload, iid, 10, 0, 2)
			if mode == "receipt_write_failure" {
				if err == nil {
					t.Fatal("storage failure swallowed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			ctx = context.Background()
			po, err := s.repo.GetByOrderItem(ctx, iid)
			if err != nil || po.UpstreamOrderID != "UP-RECEIPT" {
				t.Fatalf("lost upstream order ID: %+v %v", po, err)
			}
			fail = false
			if err := s.PollOne(ctx, po.ID); err != nil {
				t.Fatal(err)
			}
			if g.submitCalls != 1 || d.Client.OrderDelivery.Query().CountX(ctx) != 2 {
				t.Fatal("recovery repurchased or did not deliver exactly once")
			}
			if (mode == "receipt_write_failure" || mode == "cancelled_after_pending") && queries != 1 {
				t.Fatal("recovery did not query the original upstream order")
			}
		})
	}
}

func TestLegacyPurchaseKeyPreserved(t *testing.T) {
	s, d, payload, iid := submitFixture(t)
	ctx := context.Background()
	po, err := s.repo.CreatePending(ctx, iid, 1, "P1", 2, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("order_item:%d", iid)
	d.Client.ProcurementOrder.UpdateOneID(po.ID).SetDedupeKey(legacy).SaveX(ctx)
	g := &fakeGW{}
	s.gw = g
	if err := s.processItem(ctx, payload, iid, 10, 0, 2); err != nil {
		t.Fatal(err)
	}
	// An old uncertain purchase cannot be given a new key or submitted again.
	if _, err := NewAdminProcurementService(s.repo, s).RetryProcurement(ctx, &adminv1.RetryProcurementRequest{Id: po.ID}); err != nil {
		t.Fatal(err)
	}
	found, err := s.repo.GetByDownstreamOrderNo(ctx, legacy)
	if err != nil || found.ID != po.ID || found.Status != procurementorder.StatusManual || g.submitCalls != 0 {
		t.Fatalf("old uncertain purchase changed or repurchased: %+v %v", found, err)
	}
}
