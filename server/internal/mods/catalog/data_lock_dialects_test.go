package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"os"
	"strings"
	"testing"
	"time"
)

// DSNs must point at disposable, empty databases; tests create schema and fixtures.
func TestProductLockPlacementSQLDialects(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_LOCK_TEST_" + strings.ToUpper(driver) + "_DSN")
			if source == "" {
				t.Skip("isolated database not configured")
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source, MaxOpenConns: 4}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ctx := batchContext()
			if err = d.Client.Schema.Create(ctx); err != nil {
				t.Fatal(err)
			}
			svc := NewAdminCatalogService(NewProductRepoImpl(d, nil), nil, nil, nil)
			root := d.Client.Category.Create().SetName("parent").SaveX(ctx)
			child := d.Client.Category.Create().SetName("child").SetParentID(root.ID).SaveX(ctx)
			a := d.Client.Product.Create().SetName("pinned").SetSlug("dialect-pinned").SetCategoryID(child.ID).SetPrice(200).SaveX(ctx)
			b := d.Client.Product.Create().SetName("normal").SetSlug("dialect-normal").SetCategoryID(root.ID).SetPrice(100).SaveX(ctx)
			_, err = svc.SetCategoryPlacements(ctx, &adminv1.SetCategoryPlacementsRequest{CategoryId: root.ID, Items: []*adminv1.CategoryPlacementInput{{ProductId: a.ID, IsPinned: true, IsRecommended: true}}})
			if err != nil {
				t.Fatal(err)
			}
			rows, _, err := svc.repo.ListVisible(ctx, port.VisibleFilter{CategoryID: root.ID, Page: 1, PageSize: 1})
			if err != nil || len(rows) != 1 || rows[0].ID != a.ID || !rows[0].CategoryRecommend {
				t.Fatalf("default order: %v %v", rows, err)
			}
			rows, _, err = svc.repo.ListVisible(ctx, port.VisibleFilter{CategoryID: root.ID, Sort: "price_asc"})
			if err != nil || len(rows) != 2 || rows[0].ID != b.ID {
				t.Fatalf("explicit sort: %v %v", rows, err)
			}
			guarded, release, edited := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				edited <- data.Tx(ctx, d, func(tx context.Context) error {
					if _, e := data.GuardProductWrite(tx, d, a.ID); e != nil {
						return e
					}
					close(guarded)
					<-release
					return data.Client(tx, d).Product.UpdateOneID(a.ID).SetName("committed edit").Exec(tx)
				})
			}()
			select {
			case <-guarded:
			case e := <-edited:
				t.Fatalf("guard failed: %v", e)
			case <-time.After(10 * time.Second):
				t.Fatal("guard timed out")
			}
			locked := make(chan error, 1)
			go func() {
				_, e := svc.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: a.ID, IsLocked: true})
				locked <- e
			}()
			select {
			case e := <-locked:
				close(release)
				t.Fatalf("lock bypassed edit: %v", e)
			case <-time.After(80 * time.Millisecond):
			}
			close(release)
			if e := <-edited; e != nil {
				t.Fatal(e)
			}
			if e := <-locked; e != nil {
				t.Fatal(e)
			}
			if e := data.Tx(ctx, d, func(tx context.Context) error { _, e := data.GuardProductWrite(tx, d, a.ID); return e }); !data.IsProductLocked(e) {
				t.Fatalf("late write not blocked: %v", e)
			}
			reply, e := svc.BatchUpdateProductStatus(ctx, &adminv1.BatchUpdateProductStatusRequest{Ids: []uint64{a.ID, b.ID}, Status: 0})
			if e != nil || reply.Updated != 1 || reply.SkippedLocked != 1 {
				t.Fatalf("batch: %v %v", reply, e)
			}
		})
	}
}
