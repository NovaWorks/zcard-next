package migrations_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestProcurementRequestKeyUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_PROCUREMENT_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
			if driver == "sqlite" {
				source = filepath.Join(t.TempDir(), "upgrade.db")
			}
			if source == "" {
				t.Skip("requires disposable upgrade database")
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			d.DB.SetMaxOpenConns(1)
			dir, _ := migrations.FS(driver)
			files, _ := fs.Glob(dir, "*.sql")
			var upgrade []byte
			for _, name := range files {
				sql, err := fs.ReadFile(dir, name)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(name, "_procurement_request_key.sql") {
					upgrade = sql
					break
				}
				if _, err := d.DB.Exec(string(sql)); err != nil {
					t.Fatal(name, err)
				}
			}
			if len(upgrade) == 0 {
				t.Fatal("missing migration")
			}
			ctx := context.Background()
			create := func(item uint64, key string) (*ent.ProcurementOrder, error) {
				return d.Client.ProcurementOrder.Create().SetOrderItemID(item).SetConnectionID(10).SetDedupeKey(key).Save(ctx)
			}
			old, err := create(40, "order_item:40")
			if err != nil {
				t.Fatal(err)
			}
			d.Client.ProcurementOrder.UpdateOneID(old.ID).SetStatus(procurementorder.StatusManual).SetUpstreamOrderID("UP-OLD").ExecX(ctx)
			receipt := d.Client.ProcurementItem.Create().SetProcurementID(old.ID).SetUpstreamSku("P1").SetQuantity(1).SetReceivedContent([]string{"sealed-receipt"}).SaveX(ctx)
			if _, err := d.DB.Exec(string(upgrade)); err != nil {
				t.Fatal(err)
			}
			got := d.Client.ProcurementOrder.GetX(ctx, old.ID)
			if got.DedupeKey != "order_item:40" || got.Status != procurementorder.StatusManual || got.UpstreamOrderID != "UP-OLD" {
				t.Fatal("migration altered historic purchase identity or state")
			}
			gotReceipt := d.Client.ProcurementItem.GetX(ctx, receipt.ID)
			if gotReceipt.ProcurementID != old.ID || len(gotReceipt.ReceivedContent) != 1 || gotReceipt.ReceivedContent[0] != "sealed-receipt" {
				t.Fatal("migration lost the original receipt")
			}
			if _, err := create(40, "NEW-RANDOM-KEY"); !ent.IsConstraintError(err) {
				t.Fatalf("different request key allowed a second purchase: %v", err)
			}
			if _, err := create(41, "NEW-RANDOM-KEY"); err != nil {
				t.Fatal("new item blocked", err)
			}
			if _, err := create(42, "order_item:40"); !ent.IsConstraintError(err) {
				t.Fatalf("original key uniqueness lost: %v", err)
			}
			// The DB constraint is the final guard when workers simultaneously
			// allocate different random keys for the same order item.
			if driver != "sqlite" {
				d.DB.SetMaxOpenConns(8)
			}
			var wg sync.WaitGroup
			var created atomic.Int32
			for i := 0; i < 8; i++ {
				wg.Go(func() {
					_, err := create(100, fmt.Sprintf("parallel-%d", i))
					if err == nil {
						created.Add(1)
					} else if !ent.IsConstraintError(err) {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if created.Load() != 1 {
				t.Fatalf("concurrent workers created %d purchases", created.Load())
			}
		})
	}
}
