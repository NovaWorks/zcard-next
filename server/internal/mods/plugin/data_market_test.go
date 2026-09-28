package plugin

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	"path/filepath"
	"testing"
)

func TestMarketCheckpointPersistence(t *testing.T) {
	ctx := context.Background()
	d, cleanup, e := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(t.TempDir(), "checkpoint.db")}})
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	if e = d.Client.Schema.Create(ctx); e != nil {
		t.Fatal(e)
	}
	r := NewRepo(d, NewCoordinator(), nil)
	origin := "https://market.example.com"
	if e = r.configureMarket(ctx, origin); e != nil {
		t.Fatal(e)
	}
	c := mc.Catalog{Origin: origin, Revision: "10", Entries: []mc.Entry{}}
	if e = r.acceptCatalog(ctx, c); e != nil {
		t.Fatal(e)
	}
	restarted := NewRepo(d, NewCoordinator(), nil)
	old := c
	old.Revision = "9"
	if restarted.acceptCatalog(ctx, old) == nil {
		t.Fatal("restart accepted replay")
	}
	conflict := c
	conflict.Entries = []mc.Entry{{Name: "changed"}}
	if restarted.acceptCatalog(ctx, conflict) == nil {
		t.Fatal("same revision equivocation accepted")
	}
	if e = restarted.configureMarket(ctx, "https://new.example.com"); e != nil {
		t.Fatal(e)
	}
	if restarted.acceptCatalog(ctx, c) == nil {
		t.Fatal("wrong configured origin accepted")
	}
	if e = restarted.configureMarket(ctx, origin); e != nil {
		t.Fatal(e)
	}
	if restarted.acceptCatalog(ctx, old) == nil {
		t.Fatal("switch erased high water mark")
	}
	if e = restarted.acceptCatalog(ctx, c); e != nil {
		t.Fatal(e)
	}
}

func TestMarketCorruptCheckpointFailsClosed(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	d.Client.Setting.Create().SetGroup("plugin_market").SetKey("state").SetValue([]byte(`{"origin":"https://market.example.com","checkpoints":{"https://market.example.com":{"revision":"bad","hash":"bad"}}}`)).SaveX(ctx)
	if _, e := s.manager.repo.marketOrigin(ctx); e == nil {
		t.Fatal("corrupt history silently reset")
	}
	if e := s.manager.repo.configureMarket(ctx, "https://other.example.com"); e == nil {
		t.Fatal("configuration erased corrupt history")
	}
}
