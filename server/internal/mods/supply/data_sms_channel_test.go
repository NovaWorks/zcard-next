package supply

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
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
	for i := 0; i < 2; i++ {
		if _, e := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 200); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.Product.Query().CountX(ctx) != 1 || d.Client.ProductSku.Query().CountX(ctx) != 0 {
		t.Fatal("channel duplicated or materialized offers")
	}
	local := d.Client.Product.Query().OnlyX(ctx)
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
	if d.Client.Product.Query().CountX(ctx) != 1 || d.Client.Product.Query().OnlyX(ctx).Price != 0 {
		t.Fatal("sync priced ordinary product")
	}
}
