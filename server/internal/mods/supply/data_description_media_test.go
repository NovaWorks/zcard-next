package supply

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

type descriptionMediaTransport func(*http.Request) (*http.Response, error)

func (f descriptionMediaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Exercise public-address validation while keeping all image traffic local.
func descriptionMediaServer(t *testing.T) (*atomic.Int32, []byte) {
	t.Helper()
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload.Bytes())
	}))
	endpoint, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot, oldTransport := media.StorageRoot, coverClient.Transport
	media.StorageRoot = t.TempDir()
	coverClient.Transport = descriptionMediaTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "8.8.8.8" {
			return nil, fmt.Errorf("unexpected image destination: %s", r.URL.Host)
		}
		local := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = endpoint.Scheme, endpoint.Host
		local.URL, local.Host = &u, endpoint.Host
		return srv.Client().Transport.RoundTrip(local)
	})
	t.Cleanup(func() {
		coverClient.Transport = oldTransport
		media.StorageRoot = oldRoot
		srv.Close()
	})
	return &hits, payload.Bytes()
}

func descriptionMediaFixture(t *testing.T) (*AdminSupplyService, *ent.SupplyConnection) {
	t.Helper()
	s, conn := importFixture(t)
	conn = s.repo.entClient(context.Background()).SupplyConnection.UpdateOneID(conn.ID).
		SetBaseURL("https://8.8.8.8").SaveX(context.Background())
	return s, conn
}

func importDescriptionAt(t *testing.T, entry string, s *AdminSupplyService, conn *ent.SupplyConnection, p *adapter.Product) error {
	t.Helper()
	ctx := context.Background()
	switch entry {
	case "ImportOne":
		_, err := s.sync.ImportOne(ctx, conn, p, nil, PriceModeEqual, 0, 0)
		return err
	case "collect":
		_, err := s.sync.syncOne(ctx, 0, collectTask(), conn, p, nil, &TaskProgress{})
		return err
	case "durable import":
		_, err := runSelectedImportForTest(t, s, &adminv1.ImportProductsRequest{
			ConnectionId: conn.ID, Codes: []string{p.ID}, PricingMode: PriceModeEqual,
		}, map[string]adapter.Product{p.ID: *p})
		return err
	default:
		t.Fatalf("unknown import entry %q", entry)
		return nil
	}
}

func savedDescriptionProduct(t *testing.T, s *AdminSupplyService, conn *ent.SupplyConnection, code string) *ent.Product {
	t.Helper()
	ctx := context.Background()
	m, err := s.repo.GetMapping(ctx, conn.ID, code, "")
	if err != nil {
		t.Fatal(err)
	}
	return s.repo.entClient(ctx).Product.GetX(ctx, m.LocalProductID)
}

func TestDescriptionImagesSavedThroughImportAndCollect(t *testing.T) {
	for _, entry := range []string{"ImportOne", "collect", "durable import"} {
		t.Run(entry, func(t *testing.T) {
			hits, payload := descriptionMediaServer(t)
			s, conn := descriptionMediaFixture(t)
			p := &adapter.Product{ID: "description", Name: "商品", Price: 1000, IsActive: true, Stock: 8,
				DescriptionSet: true,
				Description:    `<p>商品说明</p><img src="/img.png" onerror="alert(1)"><img data-src="/lazy.png"><script>alert(1)</script>`,
			}
			if err := importDescriptionAt(t, entry, s, conn, p); err != nil {
				t.Fatal(err)
			}
			got := savedDescriptionProduct(t, s, conn, p.ID)
			if !strings.Contains(got.Description, "商品说明") || strings.Contains(got.Description, "onerror") || strings.Contains(got.Description, "<script") {
				t.Fatalf("saved description was not preserved and sanitized: %s", got.Description)
			}
			paths := data.ProductMediaPaths(got)
			if len(paths) != 2 {
				t.Fatalf("both normal and lazy image must become local media references: %s", got.Description)
			}
			for path := range paths {
				body, err := os.ReadFile(filepath.Join(media.StorageRoot, path))
				if err != nil || !bytes.Equal(body, payload) {
					t.Fatalf("description image was not stored correctly: %q, %v", path, err)
				}
			}
			if hits.Load() != 2 {
				t.Fatalf("expected exactly two image downloads, got %d", hits.Load())
			}
		})
	}
}

func TestDescriptionProtectionPreservesMediaReferences(t *testing.T) {
	for _, entry := range []string{"ImportOne", "collect", "durable import"} {
		t.Run(entry, func(t *testing.T) {
			hits, payload := descriptionMediaServer(t)
			s, conn := descriptionMediaFixture(t)
			p := &adapter.Product{ID: "protected-description", Name: "商品", Price: 1000, IsActive: true, Stock: 8}
			if err := importDescriptionAt(t, "ImportOne", s, conn, p); err != nil {
				t.Fatal(err)
			}
			old := savedDescriptionProduct(t, s, conn, p.ID)
			rel, err := media.SaveLocalIn("manual", payload, ".png")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			asset := s.repo.entClient(ctx).Media.Create().SetPath(rel).SetName("manual.png").
				SetMime("image/png").SetSize(int64(len(payload))).SetRefCount(1).SaveX(ctx)
			manual := `<p>手工说明</p><img src="/uploads/` + rel + `">`
			s.repo.entClient(ctx).Product.UpdateOneID(old.ID).SetDescription(manual).SetDescriptionProtected(true).SaveX(ctx)
			p.Description, p.DescriptionSet = `<img src="/changed.png">`, true
			if err := importDescriptionAt(t, entry, s, conn, p); err != nil {
				t.Fatal(err)
			}
			got := savedDescriptionProduct(t, s, conn, p.ID)
			if got.Description != manual || !got.DescriptionProtected || !data.ProductMediaPaths(got)[rel] {
				t.Fatalf("protected content or media reference changed: %+v", got)
			}
			if refs := s.repo.entClient(ctx).Media.GetX(ctx, asset.ID).RefCount; refs != 1 {
				t.Fatalf("protected image reference count changed: %d", refs)
			}
			if hits.Load() != 0 {
				t.Fatalf("protected description must not download replacement media: %d", hits.Load())
			}
		})
	}
}

func TestDescriptionLockedAndLightweightSyncSkipDownloads(t *testing.T) {
	t.Run("locked", func(t *testing.T) {
		hits, _ := descriptionMediaServer(t)
		s, conn := descriptionMediaFixture(t)
		p := &adapter.Product{ID: "locked-description", Name: "商品", Price: 1000, IsActive: true, Stock: 8, Description: "手工说明", DescriptionSet: true}
		if err := importDescriptionAt(t, "ImportOne", s, conn, p); err != nil {
			t.Fatal(err)
		}
		before := savedDescriptionProduct(t, s, conn, p.ID)
		p.Description = `<img src="/changed.png">`
		task, payload := enqueueTestImport(t, s, conn, []adapter.Product{*p})
		item := nextImportItem(t, s, task.ID)
		s.repo.entClient(context.Background()).Product.UpdateOneID(before.ID).SetIsLocked(true).SaveX(context.Background())
		if err := importDescriptionAt(t, "collect", s, conn, p); err != nil {
			t.Fatal(err)
		}
		if err := importDescriptionAt(t, "ImportOne", s, conn, p); !data.IsProductLocked(err) {
			t.Fatalf("locked reimport should be rejected: %v", err)
		}
		a := &importAdapter{}
		if err := s.sync.attemptImportItem(context.Background(), task, payload, conn, a, item); !data.IsProductLocked(err) {
			t.Fatalf("durable import should reject a product locked after enqueue: %v", err)
		}
		if a.quotes != 0 {
			t.Fatalf("locked durable import must skip upstream quote: %d", a.quotes)
		}
		got := savedDescriptionProduct(t, s, conn, p.ID)
		if hits.Load() != 0 || got.Description != before.Description {
			t.Fatalf("locked product fetched images or changed description: hits=%d, description=%s", hits.Load(), got.Description)
		}
	})
	for _, scope := range []string{ScopePrice, ScopeStatus, ScopeListing} {
		t.Run(scope, func(t *testing.T) {
			hits, _ := descriptionMediaServer(t)
			s, conn := descriptionMediaFixture(t)
			p := &adapter.Product{ID: "lightweight-description", Name: "商品", Price: 1000, IsActive: true, Stock: 8, Description: "手工说明", DescriptionSet: true}
			if err := importDescriptionAt(t, "ImportOne", s, conn, p); err != nil {
				t.Fatal(err)
			}
			before := savedDescriptionProduct(t, s, conn, p.ID)
			p.Description = `<img src="/changed.png">`
			if _, err := s.sync.syncOne(context.Background(), 0, &ent.SupplySyncTask{Scope: scope}, conn, p, nil, &TaskProgress{}); err != nil {
				t.Fatal(err)
			}
			got := savedDescriptionProduct(t, s, conn, p.ID)
			if hits.Load() != 0 || got.Description != before.Description {
				t.Fatalf("lightweight sync fetched images or changed description: hits=%d, description=%s", hits.Load(), got.Description)
			}
		})
	}
}
