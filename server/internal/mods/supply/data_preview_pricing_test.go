package supply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
)

type previewQuoteAdapter struct {
	*fakeUpstream
	quote func(context.Context, *adapter.Product) (*adapter.Product, error)
}

func (a *previewQuoteAdapter) QuoteProduct(ctx context.Context, p *adapter.Product) (*adapter.Product, error) {
	return a.quote(ctx, p)
}

func previewFixture(conn *ent.SupplyConnection, n int) *previewEntry {
	e := &previewEntry{identity: previewIdentity(conn), at: time.Now(), byCode: map[string]adapter.Product{}}
	cat := &adminv1.PreviewCategory{Code: "c", Name: "accounts"}
	for i := 0; i < n; i++ {
		code := fmt.Sprintf("p%d", i)
		e.byCode[code] = adapter.Product{ID: code, Name: code, Price: 9900, FactoryPrice: 9900, IsActive: true}
		cat.Products = append(cat.Products, &adminv1.PreviewProduct{Code: code, PriceCents: 9900, FactoryPriceCents: 9900})
	}
	e.categories = []*adminv1.PreviewCategory{cat}
	return e
}

func TestPreviewQuotesAreLazyAndMatchImportedCost(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "preview")
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetExchangeRate(1.5).
		SetPriceMarkupPercent(90).SetPriceMarkupAmount(777).SetPriceRoundingMode(RoundingCeilInt).SaveX(ctx)
	entry := previewFixture(conn, 3000)
	calls := 0
	var quoted *adapter.Product
	a := &previewQuoteAdapter{quote: func(ctx context.Context, p *adapter.Product) (*adapter.Product, error) {
		calls++
		if p.ID != "p1" {
			t.Fatalf("quoted unrequested product %s", p.ID)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 21*time.Second {
			t.Fatal("quote has no bounded deadline")
		}
		out := *p
		out.Price, out.FactoryPrice = 850, 850
		out.SKUs = []adapter.SKU{{ID: "small", Price: 850}, {ID: "large", Price: 1500}}
		quoted = &out
		return quoted, nil
	}}
	svc := NewAdminSupplyService(repo, nil)
	initial, err := svc.pricePreview(ctx, conn, a, entry, "")
	if err != nil || initial.Total != 3000 || calls != 0 {
		t.Fatalf("catalog should not quote eagerly: %v", err)
	}
	for _, p := range initial.Categories[0].Products {
		if p.QuoteStatus != "pending" || p.CostPriceCents != -1 || p.PriceCents != -1 {
			t.Fatal("catalog price presented as account cost")
		}
	}
	reply, err := svc.pricePreview(ctx, conn, a, entry, "p1")
	if err != nil || reply.Total != 1 || calls != 1 {
		t.Fatalf("selected quote: %v", err)
	}
	p := reply.Categories[0].Products[0]
	if p.CostPriceCents != 1275 || p.PriceCents != 850 || p.QuoteStatus != "ready" || !p.CostIsMinimum {
		t.Fatalf("wrong account cost: %v", p)
	}
	if entry.byCode["p1"].Price != 9900 || entry.categories[0].Products[1].PriceCents != 9900 {
		t.Fatal("shared catalog mutated")
	}
	writer := catalog.NewProductRepoImpl(d, nil)
	syncSvc := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	if _, err := syncSvc.ImportOne(ctx, conn, quoted, nil, PriceModeChannel, 0, 0); err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMapping(ctx, conn.ID, "p1", "")
	if actual := d.Client.Product.GetX(ctx, m.LocalProductID).FactoryPrice; actual != p.CostPriceCents {
		t.Fatalf("preview %d differs from imported cost %d", p.CostPriceCents, actual)
	}
}

func TestPreviewFailedQuoteRetryAndUnknownCode(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "failed preview")
	entry := previewFixture(conn, 2)
	svc := NewAdminSupplyService(repo, nil)
	calls := 0
	a := &previewQuoteAdapter{quote: func(context.Context, *adapter.Product) (*adapter.Product, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("upstream unavailable")
		}
		return &adapter.Product{Price: 500, FactoryPrice: 500}, nil
	}}
	if _, err := svc.pricePreview(ctx, conn, a, entry, "unknown"); err == nil || calls != 0 {
		t.Fatal("unknown code reached upstream")
	}
	r, err := svc.pricePreview(ctx, conn, a, entry, "p0")
	if err != nil {
		t.Fatal(err)
	}
	p := r.Categories[0].Products[0]
	if p.QuoteStatus != "failed" || p.CostPriceCents != -1 || p.PriceCents != -1 || p.FactoryPriceCents != -1 {
		t.Fatal("failed quote fell back to catalog price")
	}
	r, err = svc.pricePreview(ctx, conn, a, entry, "p0")
	if err != nil || r.Categories[0].Products[0].CostPriceCents != 500 || calls != 2 {
		t.Fatal("retry did not obtain a fresh quote")
	}
	if d.Client.Product.Query().CountX(ctx) != 0 {
		t.Fatal("preview wrote product data")
	}
}

func TestPreviewRejectsChangesDuringQuotation(t *testing.T) {
	for _, change := range []string{"account", "rate", "cancel"} {
		t.Run(change, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo, d := newTestRepo(t)
			conn := mustConn(t, repo, d, change)
			entry := previewFixture(conn, 1)
			a := &previewQuoteAdapter{quote: func(context.Context, *adapter.Product) (*adapter.Product, error) {
				switch change {
				case "account":
					d.Client.SupplyConnection.UpdateOneID(conn.ID).SetCredentials([]byte("changed")).ExecX(ctx)
				case "rate":
					d.Client.SupplyConnection.UpdateOneID(conn.ID).SetExchangeRate(2).ExecX(ctx)
				case "cancel":
					cancel()
				}
				return &adapter.Product{Price: 500, FactoryPrice: 500}, nil
			}}
			if _, err := NewAdminSupplyService(repo, nil).pricePreview(ctx, conn, a, entry, "p0"); err == nil {
				t.Fatal("stale/cancelled quote was returned")
			}
		})
	}
}

func TestPreviewNativeAccountCostUsesCurrentExchangeRate(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "native")
	// A public literal avoids DNS in adapter validation; the populated directory
	// cache below means no request is sent to this address.
	sealed, err := repo.SealCredentials("zcard", "https://1.1.1.1", `{"api_key":"fixture","api_secret":"fixture"}`)
	if err != nil {
		t.Fatal(err)
	}
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetBaseURL("https://1.1.1.1").SetCredentials(sealed).SaveX(ctx)
	entry := previewFixture(conn, 2)
	previewCache.Lock()
	previewCache.m[conn.ID] = *entry
	previewCache.Unlock()
	t.Cleanup(func() { previewCache.Lock(); delete(previewCache.m, conn.ID); previewCache.Unlock() })
	d.Client.SupplyConnection.UpdateOneID(conn.ID).SetExchangeRate(2).SetPriceMarkupPercent(30).ExecX(ctx)
	r, err := NewAdminSupplyService(repo, nil).PreviewProducts(ctx, &adminv1.PreviewProductsRequest{ConnectionId: conn.ID})
	if err != nil {
		t.Fatal(err)
	}
	p := r.Categories[0].Products[0]
	if p.QuoteStatus != "ready" || p.CostPriceCents != 19800 {
		t.Fatalf("cached directory used stale conversion or sale markup: %v", p)
	}
	// Exercise the actual generated HTTP query binding used by the import modal.
	srv := khttp.NewServer()
	adminv1.RegisterAdminSupplyServiceHTTPServer(srv, NewAdminSupplyService(repo, nil))
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/admin/supply/connections/%d/preview?quote_code=p1", conn.ID), nil))
	var result adminv1.PreviewProductsReply
	if response.Code != 200 {
		t.Fatalf("preview HTTP status %d: %s", response.Code, response.Body)
	}
	if err := protojson.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Categories[0].Products[0].Code != "p1" || result.Categories[0].Products[0].CostPriceCents != 19800 {
		t.Fatalf("quote_code HTTP binding or serialization failed: %v", &result)
	}
}
