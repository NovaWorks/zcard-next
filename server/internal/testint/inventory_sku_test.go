//go:build integration

package testint

import (
	"context"
	"strings"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
)

func TestSkuInventoryLifecycleMySQL(t *testing.T) { runSkuInventoryLifecycle(MySQL(t)) }
func TestSkuInventoryLifecyclePG(t *testing.T)    { runSkuInventoryLifecycle(PG(t)) }

func runSkuInventoryLifecycle(h *Harness) {
	t, d, ctx := h.T, h.Data, context.Background()
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := inventory.NewCardRepoImpl(d, cipher)
	svc := inventory.NewAdminInventoryService(repo, d)
	p := d.Client.Product.Create().SetName("membership").SetSlug("membership").SetPrice(100).SetStatus(1).SaveX(ctx)
	a := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("monthly").SetPrice(100).SetSpecValues(map[string]string{"term": "month"}).SaveX(ctx)
	b := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("yearly").SetPrice(1000).SetSpecValues(map[string]string{"term": "year"}).SaveX(ctx)
	for _, input := range []struct {
		sku   uint64
		lines []string
	}{{a.ID, []string{"MONTH-ONE", "MONTH-TWO"}}, {b.ID, []string{"YEAR-ONE"}}} {
		preview, err := svc.ImportPreview(ctx, &adminv1.ImportPreviewRequest{ProductId: p.ID, SkuId: input.sku, Lines: input.lines})
		if err != nil {
			t.Fatal(err)
		}
		batch, err := svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: input.sku, Lines: input.lines, ExpectedRevision: &preview.Revision})
		if err != nil || batch.SkuId == nil || *batch.SkuId != input.sku || batch.Imported != int32(len(input.lines)) {
			t.Fatalf("SKU import failed: %v %v", batch, err)
		}
	}
	gen, _ := id.NewGenerator(21)
	uc := &ordermod.OrderUsecase{Data: d, Inv: repo, Gen: gen, Catalog: catalog.NewProductRepoImpl(d, nil), Outbox: data.NewOutboxWriter(d)}
	result, err := uc.CreateOrder(ctx, ordermod.CreateOrderInput{UserID: 3, QueryPassword: "sku-test-password", Items: []ordermod.OrderItemInput{{ProductID: p.ID, SkuID: a.ID, Quantity: 1}, {ProductID: p.ID, SkuID: b.ID, Quantity: 1}}})
	if err != nil {
		t.Fatal("mixed SKU checkout", err)
	}
	if err := data.Tx(ctx, d, func(tx context.Context) error { return uc.MarkPaid(tx, result.OrderNo) }); err != nil {
		t.Fatal(err)
	}
	delivery := fulfillment.NewDeliveryRepoImpl(d, cipher, nil, nil)
	if err := delivery.FulfillOrder(ctx, result.OrderNo); err != nil {
		t.Fatal("fulfillment", err)
	}
	got, err := delivery.FetchDelivery(ctx, result.OrderNo, "sku-test-password", "127.0.0.1")
	if err != nil || len(got.Items) != 2 {
		t.Fatalf("delivery read failed: %v %v", got, err)
	}
	for _, item := range got.Items {
		if item.SkuName == "monthly" && !strings.HasPrefix(item.Content, "MONTH-") || item.SkuName == "yearly" && item.Content != "YEAR-ONE" || item.SkuName != "monthly" && item.SkuName != "yearly" {
			t.Fatal("delivered content did not match the ordered SKU")
		}
	}
	if n, err := repo.Stock(ctx, p.ID, a.ID); err != nil || n != 1 {
		t.Fatal("monthly remaining stock", n, err)
	}
	if _, err := uc.CreateOrder(ctx, ordermod.CreateOrderInput{UserID: 3, QueryPassword: "sku-test-password", Items: []ordermod.OrderItemInput{{ProductID: p.ID, SkuID: b.ID, Quantity: 1}}}); err == nil {
		t.Fatal("empty yearly SKU consumed monthly stock")
	}
	if d.Client.Order.Query().Where(entorder.OrderNo(result.OrderNo)).OnlyX(ctx).Status != "completed" {
		t.Fatal("fulfilled order did not complete")
	}
	// A migrated old batch has unknown origin even after its remaining cards move.
	old := d.Client.CardImport.Create().SetProductID(p.ID).SetFilename("historical").SetTotal(1).SetImported(1).SetStatus("done").SaveX(ctx)
	sealed, _ := cipher.Seal("OLD-DEFAULT", p.ID, 0)
	c := d.Client.Card.Create().SetProductID(p.ID).SetImportID(old.ID).SetContent(sealed).SetContentHash(cipher.ContentHash("OLD-DEFAULT")).SaveX(ctx)
	assignment, err := svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: a.ID, ImportId: old.ID})
	if err != nil || len(assignment.CardIds) != 1 {
		t.Fatal("historical preview", err)
	}
	if _, err := svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: a.ID, CardIds: assignment.CardIds, ExpectedRevision: &assignment.Revision}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Card.GetX(ctx, c.ID).ImportID != old.ID || d.Client.CardImport.GetX(ctx, old.ID).SkuID != nil {
		t.Fatal("assignment rewrote import provenance")
	}
	cancel, err := svc.PreviewCancelImport(ctx, &adminv1.CancelImportRequest{Id: old.ID})
	if err != nil || len(cancel.Batch.Remaining) != 1 || cancel.Batch.Remaining[0].SkuId != a.ID {
		t.Fatal("batch distribution after assignment", err)
	}
	if _, err := svc.CancelImport(ctx, &adminv1.CancelImportRequest{Id: old.ID, ExpectedRevision: cancel.Revision}); err != nil {
		t.Fatal(err)
	}
}

func TestSkuInventoryCurrentReadMySQL(t *testing.T) { runSkuInventoryCurrentRead(MySQL(t)) }
func TestSkuInventoryCurrentReadPG(t *testing.T)    { runSkuInventoryCurrentRead(PG(t)) }

func runSkuInventoryCurrentRead(h *Harness) {
	t, d, ctx := h.T, h.Data, context.Background()
	cipher, _ := inventory.NewCardCipher(make([]byte, 32))
	repo := inventory.NewCardRepoImpl(d, cipher)
	svc := inventory.NewAdminInventoryService(repo, d)
	p := d.Client.Product.Create().SetName("race").SetSlug("race").SetPrice(100).SetStatus(1).SaveX(ctx)
	sk := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("monthly").SetSpecValues(map[string]string{}).SaveX(ctx)
	held, release, imported := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		imported <- data.Tx(ctx, d, func(tx context.Context) error {
			if _, err := data.GuardProductWrite(tx, d, p.ID); err != nil {
				return err
			}
			close(held)
			<-release
			_, err := data.Client(tx, d).Card.Create().SetProductID(p.ID).SetSkuID(sk.ID).SetContent([]byte("sealed")).SetContentHash("new-stock").Save(tx)
			return err
		})
	}()
	<-held
	deleted := make(chan error, 1)
	go func() { deleted <- catalog.NewProductRepoImpl(d, nil).DeleteSku(ctx, sk.ID) }()
	select {
	case err := <-deleted:
		close(release)
		t.Fatal("deletion bypassed importer lock", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	if err := <-imported; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err == nil {
		t.Fatal("deletion used a stale pre-import snapshot")
	}
	// Reserve holds only the card row, as consumers such as lottery do. Assignment
	// must re-check the state after waiting, even while it owns the product lock.
	sealed, _ := cipher.Seal("DEFAULT-RACE", p.ID, 0)
	c := d.Client.Card.Create().SetProductID(p.ID).SetContent(sealed).SetContentHash("default-race").SaveX(ctx)
	preview, err := svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: sk.ID, CardIds: []uint64{c.ID}})
	if err != nil {
		t.Fatal(err)
	}
	held, release, consumed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		consumed <- data.Tx(ctx, d, func(tx context.Context) error {
			_, err := repo.Reserve(tx, 0, []invport.ReserveItem{{ProductID: p.ID, Quantity: 1}})
			if err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	assigned := make(chan error, 1)
	go func() {
		_, err := svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: sk.ID, CardIds: preview.CardIds, ExpectedRevision: &preview.Revision})
		assigned <- err
	}()
	select {
	case err := <-assigned:
		close(release)
		t.Fatal("assignment bypassed a locked card", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	if err := <-consumed; err != nil {
		t.Fatal(err)
	}
	if err := <-assigned; err == nil {
		t.Fatal("assignment changed a newly reserved card")
	}
	row := d.Client.Card.GetX(ctx, c.ID)
	if row.SkuID != 0 || row.Status != card.StatusReserved {
		t.Fatal("reserved card changed ownership")
	}
}
