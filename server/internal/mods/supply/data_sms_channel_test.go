package supply

import (
	"context"
	"errors"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"log/slog"
	"testing"
)

func TestChannelCollectsOneProductAndPreservesDynamicPricing(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "sms-channel")
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetDriver("zcard").SetExchangeRate(1).SaveX(ctx)
	writer := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	p := &adapter.Product{ID: "100001", Name: "Channel service", ProductKind: "sms_channel", DeliveryKind: "sms_activation", Price: 0, FactoryPrice: 0, Stock: -2, IsActive: true}
	old := d.Client.Product.Create().SetName("Old country offer").SetSlug("old-country").SetDeliveryKind("sms_activation").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("old-offer").SetPrice(100).SetStatus(1).SaveX(ctx)
	otherConn := mustConn(t, repo, d, "other-sms-source")
	other := d.Client.Product.Create().SetName("Other source offer").SetSlug("other-source-offer").SetDeliveryKind("sms_activation").SetUpstreamSourceID(otherConn.ID).SetUpstreamProductCode("old-offer").SetPrice(100).SetStatus(1).SaveX(ctx)
	ordinary := d.Client.Product.Create().SetName("Ordinary source product").SetSlug("ordinary-source").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("ordinary").SetPrice(100).SetStatus(1).SaveX(ctx)
	for i := 0; i < 2; i++ {
		if _, e := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 200); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.Product.Query().Where(product.ProductKind("sms_channel")).CountX(ctx) != 1 || d.Client.ProductSku.Query().CountX(ctx) != 0 {
		t.Fatal("channel duplicated or materialized offers")
	}
	local := d.Client.Product.Query().Where(product.ProductKind("sms_channel")).OnlyX(ctx)
	if d.Client.Product.GetX(ctx, old.ID).Status != -1 || d.Client.Product.GetX(ctx, other.ID).Status != 1 || d.Client.Product.GetX(ctx, ordinary.ID).Status != 1 {
		t.Fatal("legacy conversion lost source/type boundaries")
	}
	m, e := repo.GetMapping(ctx, conn.ID, p.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	rule, e := readProductRule(m.PricingOverride)
	if e != nil || rule.Mode != PriceModeFixed || rule.price(conn, 123) != 323 {
		t.Fatal("offer markup not saved", e)
	}
	if local.Price != 0 || local.FactoryPrice != 0 || local.ProductKind != "sms_channel" || local.Status != 1 {
		t.Fatal("wrong service projection", local)
	}
	for _, scope := range []string{ScopePrice, ScopeCollect} {
		if _, e := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope}, conn, p, nil, &TaskProgress{}); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.Product.Query().Where(product.ProductKind("sms_channel")).CountX(ctx) != 1 || d.Client.Product.GetX(ctx, local.ID).Price != 0 {
		t.Fatal("sync priced ordinary product")
	}
}

func TestChannelReimportGuardsDynamicRuleInsteadOfStaticPrice(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "channel-rule-guard")
	writer := catalog.NewProductRepoImpl(d, nil)
	syncSvc := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	svc := NewAdminSupplyService(repo, syncSvc)
	p := adapter.Product{ID: "service", Name: "Channel", ProductKind: "sms_channel", DeliveryKind: "sms_activation", Stock: -2, IsActive: true}
	if _, err := syncSvc.ImportOne(ctx, conn, &p, nil, PriceModeFixed, 0, 100); err != nil {
		t.Fatal(err)
	}
	local := d.Client.Product.Query().OnlyX(ctx)
	makeItem := func() *ent.SupplyImportItem {
		req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{p.ID}, PricingMode: PriceModeFixed, MarkupAmountCents: 200}
		key, hash, err := importRequestIdentity(req)
		if err != nil {
			t.Fatal(err)
		}
		task, err := svc.createImportTask(ctx, conn, req, map[string]adapter.Product{p.ID: p}, key, hash)
		if err != nil {
			t.Fatal(err)
		}
		return nextImportItem(t, svc, task.ID)
	}
	item := makeItem()
	if err := syncSvc.validateImportItem(ctx, conn, item); err != nil {
		t.Fatalf("zero static price blocked channel: %v", err)
	}
	// A later operator rule edit is protected even though product.Price stays 0.
	m, err := repo.GetMapping(ctx, conn.ID, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	m.PricingOverride = map[string]any{"rule": productPricingRule{Mode: PriceModeFixed, Amount: 300}}
	if err = repo.UpsertMapping(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err = syncSvc.validateImportItem(ctx, conn, item); !errors.Is(err, errImportChanged) {
		t.Fatalf("late rule edit overwritten: %v", err)
	}
	// Checkpoints made by the preceding release remain recoverable.
	item.LocalRevision = productRevision(local)
	if err = syncSvc.validateImportItem(ctx, conn, item); err != nil {
		t.Fatalf("legacy checkpoint not recoverable: %v", err)
	}
}
