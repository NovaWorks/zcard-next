package procurement

import (
	"context"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reusableGW struct {
	beforeReturn func()
	fakeGW
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (g *reusableGW) Submit(ctx context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
	g.calls.Add(1)
	if req.Quantity != 1 || req.DownstreamOrderNo == "" {
		return nil, fmt.Errorf("invalid shared purchase")
	}
	if g.entered != nil {
		g.entered <- struct{}{}
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if g.beforeReturn != nil {
		g.beforeReturn()
	}
	return g.submitRes, g.submitErr
}
func reusableFixture(t *testing.T) (*ProcureService, *data.Data, *ent.ProductDeliverySource, *reusableGW, *fulfillment.DeliveryRepoImpl) {
	t.Helper()
	repo, d := newProcureTestData(t)
	d.DB.SetMaxOpenConns(1)
	return seedReusableFixture(t, repo, d)
}
func seedReusableFixture(t *testing.T, repo *ProcureRepo, d *data.Data) (*ProcureService, *data.Data, *ent.ProductDeliverySource, *reusableGW, *fulfillment.DeliveryRepoImpl) {
	t.Helper()
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("shared").SetSlug("shared").SetFulfillmentMode("reuse").SetStatus(1).SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetName("source").SetDriver("dujiao_next").SetBaseURL("https://example.test").SetCredentials([]byte("encrypted")).SetStatus("active").SaveX(ctx)
	src := d.Client.ProductDeliverySource.Create().SetProductID(p.ID).SetCurrentKey(data.DeliverySourceKey(0, p.ID, 0)).SetConnectionID(conn.ID).SetConnectionRevision(data.ConnectionRevision(conn)).SetUpstreamProduct("P1").SaveX(ctx)
	cipher := newTestCipher(t)
	delivery := fulfillment.NewDeliveryRepoImpl(d, cipher, nil, nil)
	gw := &reusableGW{fakeGW: fakeGW{submitRes: &supplyport.PurchaseResult{Status: "delivered", Cards: []string{"shared-secret"}, Amount: 120, UpstreamOrderID: "original"}}}
	s := &ProcureService{repo: repo, cipher: cipher, attach: delivery, gw: gw, log: slog.Default()}
	return s, d, src, gw, delivery
}
func reusableBuyer(t *testing.T, d *data.Data, src *ent.ProductDeliverySource, num int) *ent.OrderItem {
	t.Helper()
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("shared-%d", num)).SetStatus("paid").SetUserID(1).SetTotalAmount(300).SaveX(ctx)
	return d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(src.ProductID).SetSkuID(src.SkuID).SetQuantity(1).SetUnitPrice(300).SetAmount(300).SetFulfillmentType(orderitem.FulfillmentTypeReuse).SetDeliverySourceID(src.ID).SaveX(ctx)
}
func TestReusableConcurrentBuyersPurchaseOnceAndRefundIsolation(t *testing.T) {
	s, d, src, gw, _ := reusableFixture(t)
	ctx := context.Background()
	first := reusableBuyer(t, d, src, 0)
	for i := 1; i < 50; i++ {
		reusableBuyer(t, d, src, i)
	}
	gw.entered = make(chan struct{}, 1)
	gw.release = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.processReusable(ctx, src.ID) }()
	<-gw.entered
	// The first buyer refunds while the shared purchase is in flight.
	d.Client.Order.UpdateOneID(first.OrderID).SetStatus("refunded").ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(first.ID).SetFulfillmentStatus("refunded").ExecX(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.processReusable(ctx, src.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(gw.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if gw.calls.Load() != 1 {
		t.Fatalf("purchased %d times", gw.calls.Load())
	}
	if n := d.Client.OrderDelivery.Query().CountX(ctx); n != 49 {
		t.Fatalf("deliveries=%d", n)
	}
	if n := d.Client.Card.Query().CountX(ctx); n != 0 {
		t.Fatalf("duplicated card pool rows=%d", n)
	}
	latest := d.Client.ProductDeliverySource.GetX(ctx, src.ID)
	if latest.PurchaseKey == nil || len(*latest.PurchaseKey) != 42 {
		t.Fatal("purchase key was not persisted as a UUID")
	}
	if latest.Status != "ready" || latest.DeliveredCount != 49 || string(latest.Content) == "shared-secret" {
		t.Fatalf("bad receipt state: %s count=%d", latest.Status, latest.DeliveredCount)
	}
	if err := s.ResumeReusable(ctx); err != nil {
		t.Fatal(err)
	}
	reusableBuyer(t, d, src, 51)
	if err := s.ResumeReusable(ctx); err != nil {
		t.Fatal(err)
	}
	if gw.calls.Load() != 1 || d.Client.OrderDelivery.Query().CountX(ctx) != 50 {
		t.Fatal("restart repurchased or failed to reuse")
	}
}
func TestReusableUnknownOutcomeNeverResubmits(t *testing.T) {
	s, d, src, gw, _ := reusableFixture(t)
	ctx := context.Background()
	reusableBuyer(t, d, src, 1)
	gw.submitErr = errors.New("upstream accepted but connection reset")
	gw.submitRes = nil
	if err := s.processReusable(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.ResumeReusable(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if gw.calls.Load() != 1 || d.Client.ProductDeliverySource.GetX(ctx, src.ID).Status != "uncertain" {
		t.Fatal("uncertain purchase retried")
	}
	// An authenticated late callback completes the SAME purchase, even after timeout.
	if err := s.confirmReusable(ctx, src.ID, []string{"late-content"}, 99, "original"); err != nil {
		t.Fatal(err)
	}
	if d.Client.OrderDelivery.Query().CountX(ctx) != 1 {
		t.Fatal("late receipt not delivered")
	}
}
func TestReusableExpiryQuotaAndImmutableVersions(t *testing.T) {
	s, d, src, _, delivery := reusableFixture(t)
	ctx := context.Background()
	encrypted, _ := s.cipher.Seal("version-one", src.ProductID, 0)
	d.Client.ProductDeliverySource.UpdateOneID(src.ID).SetStatus("ready").SetContent(encrypted).SetMaxDeliveries(1).ExecX(ctx)
	a := reusableBuyer(t, d, src, 1)
	b := reusableBuyer(t, d, src, 2)
	if err := delivery.DeliverReusable(ctx, a.OrderID, a.ID, src.ID); err != nil {
		t.Fatal(err)
	}
	if err := delivery.DeliverReusable(ctx, b.OrderID, b.ID, src.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.OrderDelivery.Query().CountX(ctx) != 1 || d.Client.OrderItem.GetX(ctx, b.ID).FulfillmentStatus != "failed" {
		t.Fatal("late paid order exceeded quota")
	}
	// Refund must not free a previously used shared-content allocation.
	d.Client.Order.UpdateOneID(a.OrderID).SetStatus("refunded").ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(a.ID).SetFulfillmentStatus("refunded").ExecX(ctx)
	n, err := data.SourceAllocations(ctx, d.Client, src.ID)
	if err != nil || n != 2 {
		t.Fatalf("allocations=%d %v", n, err)
	}
	d.Client.ProductDeliverySource.UpdateOneID(src.ID).ClearCurrentKey().SetStatus("paused").ExecX(ctx)
	other, _ := s.cipher.Seal("version-two", src.ProductID, 0)
	d.Client.ProductDeliverySource.Create().SetProductID(src.ProductID).SetCurrentKey(data.DeliverySourceKey(0, src.ProductID, 0)).SetStatus("ready").SetContent(other).SaveX(ctx)
	stored := d.Client.OrderDelivery.Query().Where(orderdelivery.ItemID(a.ID)).OnlyX(ctx)
	if stored.CardID != 0 || d.Client.OrderItem.GetX(ctx, a.ID).DeliverySourceID != src.ID {
		t.Fatal("historical version changed")
	}
	fetched, fetchErr := delivery.FetchDelivery(ctx, "shared-1", "", "127.0.0.1")
	if fetchErr != nil || len(fetched.Items) != 1 || fetched.Items[0].Content != "version-one" {
		t.Fatalf("historical fetch: %v %v", fetched, fetchErr)
	}
	old := d.Client.ProductDeliverySource.GetX(ctx, src.ID)
	plain, _ := s.cipher.Open(old.Content, src.ProductID, 0)
	if string(plain) != "version-one" {
		t.Fatal("old content mutated")
	}
	d.Client.ProductDeliverySource.UpdateOneID(src.ID).SetStatus("ready").SetMaxDeliveries(0).SetExpiresAt(time.Now().Unix() - 1).ExecX(ctx)
	if err := delivery.DeliverReusable(ctx, b.OrderID, b.ID, src.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.OrderDelivery.Query().CountX(ctx) != 1 {
		t.Fatal("expired content delivered")
	}
}

func TestSharedPurchaseCostUsesCapturedRate(t *testing.T) {
	if got := sharedPurchaseCost(125, 1.5); got != 188 {
		t.Fatalf("rate conversion=%d", got)
	}
	if sharedPurchaseCost(-1, 1) != 0 {
		t.Fatal("invalid cost accepted")
	}
}

func TestReusableEarlyCallbackSurvivesLateSubmitResponse(t *testing.T) {
	for _, upstreamID := range []string{"", "original"} {
		t.Run("order-"+upstreamID, func(t *testing.T) {
			s, d, src, gw, _ := reusableFixture(t)
			ctx := context.Background()
			reusableBuyer(t, d, src, 1)
			gw.submitRes = &supplyport.PurchaseResult{Status: "pending", UpstreamOrderID: upstreamID, Amount: 120}
			gw.beforeReturn = func() {
				if e := s.confirmReusable(ctx, src.ID, []string{"early-receipt"}, 0, ""); e != nil {
					t.Error(e)
				}
			}
			if e := s.processReusable(ctx, src.ID); e != nil {
				t.Fatal(e)
			}
			fresh := d.Client.ProductDeliverySource.GetX(ctx, src.ID)
			if fresh.Status != "ready" || fresh.DeliveredCount != 1 || fresh.CostCents != 120 || fresh.UpstreamOrderID != upstreamID {
				t.Fatalf("late response changed completed state: %s %d %d", fresh.Status, fresh.DeliveredCount, fresh.CostCents)
			}
		})
	}
}
