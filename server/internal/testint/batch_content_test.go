//go:build integration

package testint

import (
	"context"
	"sync"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

func TestBatchContentMySQL(t *testing.T) { runBatchContent(MySQL(t)) }
func TestBatchContentPG(t *testing.T)    { runBatchContent(PG(t)) }
func runBatchContent(h *Harness) {
	t := h.T
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 1, Realm: authn.RealmAdmin})
	c := h.Data.Client
	repo := catalog.NewProductRepoImpl(h.Data, nil)
	svc := catalog.NewAdminCatalogService(repo, nil, nil, nil)
	image := c.Media.Create().SetName("shared").SetPath("batch.png").SetMime("image/png").SetSize(1).SaveX(ctx)
	a := c.Product.Create().SetName("A").SetSlug("a").SetUpstreamSourceID(1).SetUpstreamProductCode("a").SetPrice(42).SaveX(ctx)
	b := c.Product.Create().SetName("B").SetSlug("b").SaveX(ctx)
	preview, err := svc.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: []uint64{a.ID, b.ID}, Patch: &adminv1.ProductContentPatch{CoverAction: 1, Cover: "/uploads/batch.png", DescriptionAction: 1, Description: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	req := &adminv1.BatchProductContentRequest{RequestId: preview.RequestId}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := svc.BatchUpdateProductContent(ctx, req); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.Media.GetX(ctx, image.ID).RefCount != 2 || c.AuditLog.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate submission changed reference or audit count")
	}
	if _, _, err := repo.UpsertUpstreamProduct(ctx, port.UpstreamProductInput{UpstreamSyncedAt: time.Now(), ConnectionID: 1, UpstreamProductCode: "a", Name: "upstream", Cover: "https://example.com/new.png", Description: "remote", DescriptionSet: true, Price: 55, Status: 1}); err != nil {
		t.Fatal(err)
	}
	got := c.Product.GetX(ctx, a.ID)
	if got.Cover != "/uploads/batch.png" || got.Description != "local" || got.Price != 55 {
		t.Fatal("upstream protection or price propagation failed")
	}
	restore, err := svc.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: []uint64{a.ID}, Patch: &adminv1.ProductContentPatch{DescriptionAction: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: restore.RequestId}); err != nil {
		t.Fatal(err)
	}
	// Pause after the upstream request's initial snapshot, before it acquires
	// the product guard. Pausing a mutation now would hold that guard while
	// waiting for the competing batch, creating an artificial deadlock.
	// The sync must re-read content protection after it acquires the row lock.
	type syncKey struct{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c.Product.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, q)
			if ctx.Value(syncKey{}) != nil && err == nil {
				once.Do(func() { close(entered); <-release })
			}
			return value, err
		})
	}))
	syncErr := make(chan error, 1)
	go func() {
		_, _, err := repo.UpsertUpstreamProduct(context.WithValue(ctx, syncKey{}, true), port.UpstreamProductInput{UpstreamSyncedAt: time.Now(), ConnectionID: 1, UpstreamProductCode: "a", Name: "upstream", Cover: "https://example.com/late.png", Description: "late", DescriptionSet: true, Price: 66, Status: 1})
		syncErr <- err
	}()
	<-entered
	newer, err := svc.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: []uint64{a.ID}, Patch: &adminv1.ProductContentPatch{DescriptionAction: 1, Description: "local newest"}})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if _, err = svc.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: newer.RequestId}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-syncErr; err != nil {
		t.Fatal(err)
	}
	if c.Product.GetX(ctx, a.ID).Description != "local newest" {
		t.Fatal("stale upstream request overwrote local content")
	}

	preview, err = svc.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: []uint64{a.ID, b.ID}, Patch: &adminv1.ProductContentPatch{DescriptionAction: 1, Description: "next"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Product.UpdateOneID(b.ID).SetDescription("changed elsewhere").ExecX(ctx)
	if _, err = svc.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: preview.RequestId}); err == nil {
		t.Fatal("stale write allowed")
	}
	if c.Product.GetX(ctx, a.ID).Description != "local newest" {
		t.Fatal("partial update escaped transaction")
	}
}
