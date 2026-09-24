package supply

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"log/slog"
	"testing"
)

func TestLockedProductSkipsAllSyncScopesAndReimport(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "locked")
	writer := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	p := pricingProduct()
	if _, e := s.ImportOne(ctx, conn, p, nil, PriceModeEqual, 0, 0); e != nil {
		t.Fatal(e)
	}
	mapping, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
	before := d.Client.Product.UpdateOneID(mapping.LocalProductID).SetIsLocked(true).SaveX(ctx)
	skus := d.Client.ProductSku.Query().Where(productsku.ProductID(before.ID)).AllX(ctx)
	p.Name = "upstream changed"
	p.Price = 9876
	p.FactoryPrice = 8888
	p.SKUs[0].Price = 7777
	p.IsActive = false
	for _, scope := range []string{ScopeCollect, ScopePrice, ScopeStatus} {
		progress := &TaskProgress{}
		if _, e := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope, ForceReprice: true}, conn, p, nil, progress); e != nil {
			t.Fatal(e)
		}
		if progress.ManualSkipped != 1 {
			t.Fatal("lock not counted as skip")
		}
	}
	if _, e := s.ImportOne(ctx, conn, p, nil, PriceModeEqual, 0, 0); !data.IsProductLocked(e) {
		t.Fatalf("manual reimport: %v", e)
	}
	if _, e := writer.ShelveOffMissing(ctx, conn.ID, nil); e != nil {
		t.Fatal(e)
	}
	got := d.Client.Product.GetX(ctx, before.ID)
	if got.Name != before.Name || got.Price != before.Price || got.FactoryPrice != before.FactoryPrice || got.Status != before.Status || got.Cover != before.Cover {
		t.Fatal("sync mutated locked product")
	}
	if d.Client.ProductSku.GetX(ctx, skus[0].ID).Price != skus[0].Price {
		t.Fatal("sync repriced locked SKU")
	}
}
