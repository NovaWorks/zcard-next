package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cardimport"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

func inventorySKUFixture(t *testing.T) (*AdminInventoryService, *data.Data, *ent.Product, *ent.ProductSku, *ent.ProductSku) {
	t.Helper()
	r, d := newTestRepo(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("subscription").SetSlug("subscription").SetPrice(100).SetStockType("card").SaveX(ctx)
	a := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName("monthly").SaveX(ctx)
	b := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetProductID(p.ID).SetName("annual").SaveX(ctx)
	return NewAdminInventoryService(r, d), d, p, a, b
}
func uint64Pointer(n uint64) *uint64 { return &n }
func int64Pointer(n int64) *int64    { return &n }
func putInventoryCard(t *testing.T, s *AdminInventoryService, p *ent.Product, sku *uint64, value string, status card.Status, batchID uint64) *ent.Card {
	t.Helper()
	ct, e := s.repo.Cipher.Seal(value, p.ID, p.SubsiteID)
	if e != nil {
		t.Fatal(e)
	}
	c := s.data.Client.Card.Create().SetSubsiteID(p.SubsiteID).SetProductID(p.ID).SetContent(ct).SetContentHash(s.repo.Cipher.ContentHash(value)).SetStatus(status)
	if sku != nil {
		c.SetSkuID(*sku)
	}
	if batchID > 0 {
		c.SetImportID(batchID)
	}
	return c.SaveX(context.Background())
}
func requireInventoryReason(t *testing.T, err error, want string) {
	t.Helper()
	if errors.Reason(err) != want {
		t.Fatalf("reason=%q want %q: %v", errors.Reason(err), want, err)
	}
}

func TestSKUImportRoundTripAndFiltering(t *testing.T) {
	svc, d, p, a, b := inventorySKUFixture(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 77})
	for _, sku := range []*ent.ProductSku{a, b} {
		preview, e := svc.ImportPreview(ctx, &adminv1.ImportPreviewRequest{ProductId: p.ID, SkuId: sku.ID, Lines: []string{sku.Name + "-1", sku.Name + "-2"}})
		if e != nil {
			t.Fatal(e)
		}
		if preview.SkuId != sku.ID || preview.SkuName != sku.Name {
			t.Fatal(preview)
		}
		batch, e := svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: sku.ID, Lines: []string{sku.Name + "-1", sku.Name + "-2"}, ExpectedRevision: &preview.Revision})
		if e != nil {
			t.Fatal(e)
		}
		if batch.Imported != 2 || batch.SkuId == nil || *batch.SkuId != sku.ID || batch.SkuName != sku.Name {
			t.Fatal(batch)
		}
		imp := d.Client.CardImport.GetX(ctx, batch.Id)
		if imp.OperatorID != 77 || imp.SkuID == nil || *imp.SkuID != sku.ID {
			t.Fatal(imp)
		}
	}
	// Duplicate content remains forbidden across SKUs of the same product.
	duplicate, e := svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: b.ID, Lines: []string{a.Name + "-1"}})
	if e != nil {
		t.Fatal(e)
	}
	if duplicate.Imported != 0 || duplicate.Skipped != 1 {
		t.Fatal(duplicate)
	}
	putInventoryCard(t, svc, p, nil, "legacy-null", card.StatusAvailable, 0)
	putInventoryCard(t, svc, p, uint64Pointer(0), "legacy-zero", card.StatusAvailable, 0)
	for _, tc := range []struct {
		sku  *uint64
		want int64
	}{{nil, 6}, {uint64Pointer(0), 2}, {&a.ID, 2}, {&b.ID, 2}} {
		list, e := svc.ListCards(ctx, &adminv1.ListCardsRequest{ProductId: p.ID, SkuId: tc.sku})
		if e != nil {
			t.Fatal(e)
		}
		if list.Total != tc.want || int64(len(list.Cards)) != tc.want {
			t.Fatal(list)
		}
		exported, e := svc.ExportCards(ctx, &adminv1.ExportCardsRequest{ProductId: p.ID, SkuId: tc.sku})
		if e != nil {
			t.Fatal(e)
		}
		if int64(len(exported.Lines)) != tc.want {
			t.Fatal(exported)
		}
	}
	list, e := svc.ListCards(ctx, &adminv1.ListCardsRequest{ProductId: p.ID, SkuId: &a.ID, PageSize: 1})
	if e != nil {
		t.Fatal(e)
	}
	if list.Total != 2 || len(list.Cards) != 1 || list.Cards[0].SkuName != a.Name || list.Cards[0].ImportId == 0 {
		t.Fatal(list)
	}
	batches, e := svc.ListImports(ctx, &adminv1.ListImportsRequest{ProductId: p.ID, SkuId: &a.ID})
	if e != nil {
		t.Fatal(e)
	}
	if len(batches.Batches) != 1 || len(batches.Batches[0].Remaining) != 1 || batches.Batches[0].Remaining[0].Available != 2 {
		t.Fatal(batches)
	}
	targets, e := svc.GetTargets(ctx, &adminv1.InventoryTargetsRequest{ProductId: p.ID})
	if e != nil {
		t.Fatal(e)
	}
	if !targets.HasSkus || len(targets.Targets) != 3 || targets.Targets[0].CanImport || targets.Targets[0].Available != 2 {
		t.Fatal(targets)
	}
	reserved, e := svc.repo.Reserve(ctx, 0, []port.ReserveItem{{ProductID: p.ID, SkuID: a.ID, Quantity: 2}})
	if e != nil || len(reserved.Cards) != 2 {
		t.Fatal(reserved, e)
	}
	if _, e = svc.repo.Reserve(ctx, 0, []port.ReserveItem{{ProductID: p.ID, SkuID: a.ID, Quantity: 1}}); e == nil {
		t.Fatal("used a different SKU or default pool as fallback")
	}
}
func TestSKUImportAdmissionAndRevision(t *testing.T) {
	svc, d, p, a, b := inventorySKUFixture(t)
	ctx := context.Background()
	_, e := svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, Lines: []string{"missing"}})
	requireInventoryReason(t, e, "inventory.SKU_REQUIRED")
	foreign := d.Client.Product.Create().SetName("other").SetSlug("other").SetPrice(1).SaveX(ctx)
	_, e = svc.ImportPreview(ctx, &adminv1.ImportPreviewRequest{ProductId: foreign.ID, SkuId: a.ID, Lines: []string{"foreign"}})
	requireInventoryReason(t, e, "inventory.SKU_INVALID")
	stale := p.LockVersion
	d.Client.Product.UpdateOneID(p.ID).AddLockVersion(1).ExecX(ctx)
	_, e = svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: a.ID, Lines: []string{"stale"}, ExpectedRevision: &stale})
	requireInventoryReason(t, e, "inventory.CONFIG_CHANGED")
	for _, mode := range []string{"manual", "reuse"} {
		d.Client.ProductSku.UpdateOneID(a.ID).SetFulfillmentMode(mode).ExecX(ctx)
		_, e = svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: a.ID, Lines: []string{mode}})
		requireInventoryReason(t, e, "inventory.TARGET_UNAVAILABLE")
	}
	// Provenance upstream does not forbid an operator-owned local SKU override.
	d.Client.Product.UpdateOneID(p.ID).SetUpstreamSourceID(123).ExecX(ctx)
	d.Client.ProductSku.UpdateOneID(a.ID).SetFulfillmentMode("local").ExecX(ctx)
	if _, e = svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: a.ID, Lines: []string{"local-override"}}); e != nil {
		t.Fatal(e)
	}
	_, e = svc.ImportPreview(ctx, &adminv1.ImportPreviewRequest{ProductId: p.ID, SkuId: b.ID, Lines: []string{"upstream"}})
	requireInventoryReason(t, e, "inventory.TARGET_UNAVAILABLE")
	if got := d.Client.Card.Query().CountX(ctx); got != 1 {
		t.Fatalf("rejected imports wrote %d cards", got)
	}
}
func TestSKUImportTenantAndDefaultCompatibility(t *testing.T) {
	r, d := newTestRepo(t)
	svc := NewAdminInventoryService(r, d)
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{SubsiteID: 12})
	p := d.Client.Product.Create().SetSubsiteID(12).SetName("tenant").SetSlug("tenant").SetPrice(100).SetStockType("card").SaveX(ctx)
	a := d.Client.ProductSku.Create().SetSpecValues(map[string]string{}).SetSubsiteID(12).SetProductID(p.ID).SetName("term").SaveX(ctx)
	_, e := svc.ImportConfirm(context.Background(), &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: a.ID, Lines: []string{"wrong-tenant"}})
	requireInventoryReason(t, e, "catalog.PRODUCT_NOT_FOUND")
	if _, e = svc.ImportConfirm(ctx, &adminv1.ImportConfirmRequest{ProductId: p.ID, SkuId: a.ID, Lines: []string{"tenant-secret"}}); e != nil {
		t.Fatal(e)
	}
	row := d.Client.Card.Query().OnlyX(ctx)
	imp := d.Client.CardImport.Query().OnlyX(ctx)
	if row.SubsiteID != 12 || imp.SubsiteID != 12 {
		t.Fatal("tenant not propagated")
	}
	if plain, e := r.Cipher.Open(row.Content, p.ID, 12); e != nil || plain != "tenant-secret" {
		t.Fatal(plain, e)
	}
	if _, e = r.Cipher.Open(row.Content, p.ID, 0); e == nil {
		t.Fatal("wrong AAD accepted")
	}
	if _, e = svc.ListCards(context.Background(), &adminv1.ListCardsRequest{ProductId: p.ID}); e == nil {
		t.Fatal("cross tenant cards visible")
	}
	all, e := svc.ListImports(context.Background(), &adminv1.ListImportsRequest{})
	if e != nil || len(all.Batches) != 0 {
		t.Fatal(all, e)
	}
	plain := d.Client.Product.Create().SetName("default").SetSlug("default").SetStockType("card").SaveX(context.Background())
	batch, e := svc.ImportConfirm(context.Background(), &adminv1.ImportConfirmRequest{ProductId: plain.ID, Lines: []string{"default-card"}})
	if e != nil || batch.SkuId == nil || *batch.SkuId != 0 {
		t.Fatal(batch, e)
	}
	old := d.Client.CardImport.Create().SetProductID(plain.ID).SetFilename("historic").SaveX(context.Background())
	oldList, e := svc.ListImports(context.Background(), &adminv1.ListImportsRequest{ProductId: plain.ID, LegacyOnly: true})
	if e != nil || len(oldList.Batches) != 1 || oldList.Batches[0].Id != old.ID || oldList.Batches[0].SkuId != nil {
		t.Fatal(oldList, e)
	}
}
func TestAssignPreservesBatchAndCancellationScope(t *testing.T) {
	svc, d, p, a, b := inventorySKUFixture(t)
	ctx := context.Background()
	origin := d.Client.CardImport.Create().SetProductID(p.ID).SetFilename("historic").SetStatus(cardimport.StatusDone).SaveX(ctx)
	first := putInventoryCard(t, svc, p, nil, "monthly", card.StatusAvailable, origin.ID)
	second := putInventoryCard(t, svc, p, uint64Pointer(0), "annual", card.StatusAvailable, origin.ID)
	reserved := putInventoryCard(t, svc, p, nil, "reserved", card.StatusReserved, origin.ID)
	used := putInventoryCard(t, svc, p, nil, "used", card.StatusUsed, origin.ID)
	disabled := putInventoryCard(t, svc, p, nil, "disabled", card.StatusDisabled, origin.ID)
	for i, tc := range []struct{ id, sku uint64 }{{first.ID, a.ID}, {second.ID, b.ID}} {
		pre, e := svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: tc.sku, CardIds: []uint64{tc.id}})
		if e != nil {
			t.Fatal(e)
		}
		result, e := svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: tc.sku, CardIds: pre.CardIds, ExpectedRevision: &pre.Revision, RequestKey: fmt.Sprintf("assignment-%d", i)})
		if e != nil || result.Assigned != 1 {
			t.Fatal(result, e)
		}
		row := d.Client.Card.GetX(ctx, tc.id)
		if row.ImportID != origin.ID || row.SkuID != tc.sku {
			t.Fatal(row)
		}
	}
	if imp := d.Client.CardImport.GetX(ctx, origin.ID); imp.SkuID != nil || imp.SkuName != "" {
		t.Fatal("assignment rewrote provenance")
	}
	pre, e := svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: a.ID, ImportId: origin.ID})
	if e != nil || pre.EligibleCount != 0 || pre.ExcludedCount != 5 {
		t.Fatal(pre, e)
	}
	cancel, e := svc.PreviewCancelImport(ctx, &adminv1.CancelImportRequest{Id: origin.ID})
	if e != nil || len(cancel.Batch.Remaining) != 3 {
		t.Fatal(cancel, e)
	}
	d.Client.Card.UpdateOneID(second.ID).AddVersion(1).ExecX(ctx)
	_, e = svc.CancelImport(ctx, &adminv1.CancelImportRequest{Id: origin.ID, ExpectedRevision: cancel.Revision})
	requireInventoryReason(t, e, "inventory.BATCH_CHANGED")
	if d.Client.Card.Query().CountX(ctx) != 5 {
		t.Fatal("conflicted cancel deleted cards")
	}
	cancel, e = svc.PreviewCancelImport(ctx, &adminv1.CancelImportRequest{Id: origin.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.CancelImport(ctx, &adminv1.CancelImportRequest{Id: origin.ID, ExpectedRevision: cancel.Revision}); e != nil {
		t.Fatal(e)
	}
	if d.Client.Card.Query().CountX(ctx) != 3 {
		t.Fatal("cancellation did not remove both assigned available cards")
	}
	for _, id := range []uint64{reserved.ID, used.ID, disabled.ID} {
		if _, e = d.Client.Card.Get(ctx, id); e != nil {
			t.Fatal("canceled protected card", e)
		}
	}
	if d.Client.SecurityAuditLog.Query().CountX(ctx) != 2 {
		t.Fatal("assignments missing audit")
	}
	for _, entry := range d.Client.SecurityAuditLog.Query().AllX(ctx) {
		raw, e := json.Marshal(entry.Metadata)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(raw), "assignment-") {
			t.Fatal("audit retained free-form request key")
		}
		if _, ok := entry.Metadata["request_key"]; ok {
			t.Fatal("audit retained original request key")
		}
		if digest, ok := entry.Metadata["request_key_hash"].(string); !ok || len(digest) != 64 {
			t.Fatal("audit lacks safe request correlation")
		}
	}
}
func TestAssignRejectsChangedOrProtectedCardsAtomically(t *testing.T) {
	svc, d, p, a, _ := inventorySKUFixture(t)
	ctx := context.Background()
	first := putInventoryCard(t, svc, p, nil, "first", card.StatusAvailable, 0)
	second := putInventoryCard(t, svc, p, nil, "second", card.StatusAvailable, 0)
	pre, e := svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: a.ID, CardIds: []uint64{first.ID, second.ID}})
	if e != nil {
		t.Fatal(e)
	}
	d.Client.Card.UpdateOneID(second.ID).SetStatus(card.StatusReserved).ExecX(ctx)
	_, e = svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: a.ID, CardIds: pre.CardIds, ExpectedRevision: &pre.Revision})
	requireInventoryReason(t, e, "inventory.CARDS_CHANGED")
	if d.Client.Card.GetX(ctx, first.ID).SkuID != 0 {
		t.Fatal("partial assignment committed")
	}
	d.Client.Card.UpdateOneID(second.ID).SetStatus(card.StatusAvailable).SetOwnerID(9).ExecX(ctx)
	_, e = svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: a.ID, CardIds: []uint64{second.ID}})
	requireInventoryReason(t, e, "inventory.CARDS_CHANGED")
	d.Client.Card.UpdateOneID(second.ID).SetOwnerID(0).SetLockedAt(time.Now()).ExecX(ctx)
	_, e = svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: a.ID, CardIds: []uint64{second.ID}})
	requireInventoryReason(t, e, "inventory.CARDS_CHANGED")
	if _, e = svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: a.ID, CardIds: []uint64{first.ID}}); e == nil {
		t.Fatal("missing preview revision accepted")
	}
}
func TestAssignAndReserveDoNotChangeConsumedCard(t *testing.T) {
	svc, d, p, a, _ := inventorySKUFixture(t)
	ctx := context.Background()
	row := putInventoryCard(t, svc, p, nil, "race", card.StatusAvailable, 0)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, e := svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: a.ID, CardIds: []uint64{row.ID}, ExpectedRevision: int64Pointer(p.LockVersion)})
		results <- e == nil
	}()
	go func() {
		defer wg.Done()
		<-start
		e := data.Tx(ctx, d, func(tx context.Context) error {
			_, e := svc.repo.Reserve(tx, 0, []port.ReserveItem{{ProductID: p.ID, Quantity: 1}})
			return e
		})
		results <- e == nil
	}()
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for ok := range results {
		if ok {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("assignment/reservation successes=%d, want one", success)
	}
	got := d.Client.Card.GetX(ctx, row.ID)
	if got.Status == card.StatusReserved && got.SkuID != 0 {
		t.Fatal("consumed default card changed SKU")
	}
}

func TestAssignmentDoesNotReuseAwardedCardsAndShowsOrphanInventory(t *testing.T) {
	svc, d, p, a, b := inventorySKUFixture(t)
	ctx := context.Background()
	old := d.Client.CardImport.Create().SetProductID(p.ID).SetFilename("old").SaveX(ctx)
	row := putInventoryCard(t, svc, p, nil, "awarded-reference", card.StatusAvailable, old.ID)
	d.Client.LotteryDraw.Create().SetActivityID(1).SetUserID(1).SetDrawNo("inventory-award").SetRequestKey("inventory-award").SetActivityName("historical").SetStatus("delivered").SetRevision(1).SetCardID(row.ID).SetProductID(p.ID).SaveX(ctx)
	pre, e := svc.AssignPreview(ctx, &adminv1.AssignPreviewRequest{ProductId: p.ID, SkuId: a.ID, ImportId: old.ID})
	if e != nil || pre.EligibleCount != 0 || pre.ExcludedCount != 1 {
		t.Fatal(pre, e)
	}
	_, e = svc.AssignCards(ctx, &adminv1.AssignCardsRequest{ProductId: p.ID, SkuId: a.ID, CardIds: []uint64{row.ID}, ExpectedRevision: int64Pointer(p.LockVersion)})
	requireInventoryReason(t, e, "inventory.CARDS_CHANGED")
	putInventoryCard(t, svc, p, &b.ID, "old-deleted-sku", card.StatusAvailable, 0)
	d.Client.ProductSku.DeleteOneID(b.ID).ExecX(ctx)
	targets, e := svc.GetTargets(ctx, &adminv1.InventoryTargetsRequest{ProductId: p.ID})
	if e != nil {
		t.Fatal(e)
	}
	for _, target := range targets.Targets {
		if target.SkuId == b.ID {
			if target.CanImport || target.Available != 1 || target.Reason == "" {
				t.Fatal(target)
			}
			return
		}
	}
	t.Fatal("orphan inventory was not visible for management")
}

// Exercise generated BindQuery/JSON encoding, not just pointer construction in Go.
func TestInventoryHTTPOptionalSkuPresence(t *testing.T) {
	svc, d, p, a, b := inventorySKUFixture(t)
	ctx := context.Background()
	legacy := d.Client.CardImport.Create().SetProductID(p.ID).SetFilename("old").SaveX(ctx)
	zero := d.Client.CardImport.Create().SetProductID(p.ID).SetFilename("default").SetSkuID(0).SetSkuName("default").SaveX(ctx)
	chosen := d.Client.CardImport.Create().SetProductID(p.ID).SetFilename("chosen").SetSkuID(a.ID).SetSkuName(a.Name).SaveX(ctx)
	putInventoryCard(t, svc, p, nil, "http-null", card.StatusAvailable, legacy.ID)
	putInventoryCard(t, svc, p, uint64Pointer(0), "http-zero", card.StatusAvailable, zero.ID)
	putInventoryCard(t, svc, p, &a.ID, "http-month", card.StatusAvailable, chosen.ID)
	putInventoryCard(t, svc, p, &b.ID, "http-year", card.StatusAvailable, 0)
	server := khttp.NewServer()
	adminv1.RegisterAdminInventoryServiceHTTPServer(server, svc)
	get := func(path string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s status=%d %s", path, w.Code, w.Body.String())
		}
		var reply map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &reply); e != nil {
			t.Fatal(e)
		}
		return reply
	}
	base := fmt.Sprintf("?product_id=%d", p.ID)
	for _, tc := range []struct {
		query string
		total float64
	}{{"", 4}, {"&sku_id=0", 2}, {fmt.Sprintf("&sku_id=%d", a.ID), 1}} {
		reply := get("/api/v1/admin/inventory/cards" + base + tc.query)
		if reply["total"] != tc.total {
			t.Fatalf("query %s total=%v want %v", tc.query, reply["total"], tc.total)
		}
	}
	all := get("/api/v1/admin/inventory/imports" + base)
	if len(all["batches"].([]any)) != 3 {
		t.Fatal(all)
	}
	for _, tc := range []struct {
		query string
		batch uint64
		sku   *uint64
	}{{"&legacy_only=true", legacy.ID, nil}, {"&sku_id=0", zero.ID, uint64Pointer(0)}, {fmt.Sprintf("&sku_id=%d", a.ID), chosen.ID, &a.ID}} {
		reply := get("/api/v1/admin/inventory/imports" + base + tc.query)
		rows := reply["batches"].([]any)
		if len(rows) != 1 {
			t.Fatal(reply)
		}
		row := rows[0].(map[string]any)
		if row["id"] != float64(tc.batch) {
			t.Fatal(row)
		}
		sku, exists := row["sku_id"]
		if tc.sku == nil {
			if exists {
				t.Fatal("historical unknown target encoded as default", row)
			}
		} else if !exists || sku != float64(*tc.sku) {
			t.Fatal("explicit target presence lost", row)
		}
	}
}
