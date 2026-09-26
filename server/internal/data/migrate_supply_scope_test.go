package data

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyorder"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestSupplyOrderAccountScopeUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "upgrade.db")
	d, cleanup, err := NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: dsn}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	fsys, err := migrations.FS("sqlite")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := embedToMemDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	files, err := dir.Files()
	if err != nil {
		t.Fatal(err)
	}
	before := -1
	for i, f := range files {
		if strings.Contains(f.Name(), "supply_order_account_scope") {
			before = i
			break
		}
	}
	if before < 1 {
		t.Fatal("scope migration missing")
	}
	driver, err := atlasDriver(d.DB, db.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := newRevisionReadWriter(d.DB, db.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := atlasmigrate.NewExecutor(driver, dir, revisions)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.ExecuteN(ctx, before); err != nil {
		t.Fatal(err)
	}
	create := func(account uint64) (*ent.SupplyOrder, error) {
		return d.Client.SupplyOrder.Create().SetAccountID(account).SetDownstreamOrderNo("legacy-no").SetItems([]map[string]any{{"product_id": 1, "quantity": 1, "card_ids": []uint64{42}}}).SetAmount(1000).SetStatus(supplyorder.StatusFulfilled).Save(ctx)
	}
	old, err := create(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := create(2); !ent.IsConstraintError(err) {
		t.Fatalf("old global uniqueness missing: %v", err)
	}
	if err := ApplyMigrations(ctx, d.DB, db.SQLite, dsn); err != nil {
		t.Fatal(err)
	}
	restored, err := d.Client.SupplyOrder.Get(ctx, old.ID)
	if err != nil || restored.AccountID != 1 || restored.DownstreamOrderNo != "legacy-no" || restored.Amount != 1000 || restored.Status != supplyorder.StatusFulfilled || len(restored.Items) != 1 || len(restored.Items[0]["card_ids"].([]any)) != 1 {
		t.Fatalf("legacy order changed: %v %v", restored, err)
	}
	if _, err := create(2); err != nil {
		t.Fatalf("different account collision after upgrade: %v", err)
	}
	if _, err := create(1); !ent.IsConstraintError(err) {
		t.Fatalf("same account duplicate accepted: %v", err)
	}
	if err := ApplyMigrations(ctx, d.DB, db.SQLite, dsn); err != nil {
		t.Fatalf("migration retry: %v", err)
	}
}
