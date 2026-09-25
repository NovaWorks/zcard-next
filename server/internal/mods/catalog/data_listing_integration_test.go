//go:build integration

package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
	"sync"
	"testing"
	"time"
)

func TestListingSQLDialects(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *testint.Harness{"mysql": testint.MySQL, "postgres": testint.PG} {
		t.Run(name, func(t *testing.T) {
			h := open(t)
			d := h.Data
			ctx := batchContext()
			s := NewAdminCatalogService(NewProductRepoImpl(d, nil), nil, nil, nil)
			conn := d.Client.SupplyConnection.Create().SetName("test").SetDriver("zcard").SetBaseURL("https://example.com").SetCredentials([]byte("test")).SaveX(ctx)
			p := d.Client.Product.Create().SetName("listing").SetSlug("listing").SetPrice(100).SetStatus(2).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("p").SaveX(ctx)
			preview, e := s.ManageProductListing(ctx, &adminv1.ManageProductListingRequest{Action: "enable", Ids: []uint64{p.ID}})
			if e != nil {
				t.Fatal(e)
			}
			req := &adminv1.ManageProductListingRequest{Action: "enable", Apply: true, Revisions: map[uint64]string{p.ID: preview.Items[0].Revision}}
			r, e := s.ManageProductListing(ctx, req)
			if e != nil || r.Changed != 1 {
				t.Fatal(r, e)
			}
			at := time.Now().Add(-time.Minute)
			d.Client.Product.UpdateOneID(p.ID).SetListingChangedAt(at.Add(-time.Second).UnixMilli()).SetListingZeroSince(at.Add(-time.Minute).UnixMilli()).ExecX(ctx)
			e = data.Tx(ctx, d, func(ctx context.Context) error {
				p, e := data.GuardProductWrite(ctx, d, p.ID)
				if e != nil {
					return e
				}
				return data.ObserveListing(ctx, d, p, 0, true, at)
			})
			if e != nil {
				t.Fatal(e)
			}
			if d.Client.Product.GetX(ctx, p.ID).Status != 0 {
				t.Fatal("not shelved off")
			}
			// Concurrent observations are serialized; only one transition/audit occurs.
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					e := data.Tx(ctx, d, func(ctx context.Context) error {
						p, e := data.GuardProductWrite(ctx, d, p.ID)
						if e != nil {
							return e
						}
						return data.ObserveListing(ctx, d, p, 5, true, at.Add(time.Second))
					})
					if e != nil {
						t.Error(e)
					}
				}()
			}
			wg.Wait()
			if d.Client.Product.GetX(ctx, p.ID).Status != 2 {
				t.Fatal("hidden status not restored")
			}
			if n := d.Client.AuditLog.Query().CountX(ctx); n != 3 {
				t.Fatalf("duplicate changes: audit count %d", n)
			}
			// A response fetched before lock/unlock may not resume the product.
			before := time.Now().Add(-time.Second)
			_, e = s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, IsLocked: true})
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, ExpectedVersion: 1})
			if e != nil {
				t.Fatal(e)
			}
			e = data.Tx(ctx, d, func(ctx context.Context) error {
				p, e := data.GuardProductWrite(ctx, d, p.ID)
				if e != nil {
					return e
				}
				return data.ObserveListing(ctx, d, p, 0, false, before)
			})
			if e != nil {
				t.Fatal(e)
			}
			if d.Client.Product.GetX(ctx, p.ID).Status != 2 {
				t.Fatal("late observation after unlock was accepted")
			}
		})
	}
}
