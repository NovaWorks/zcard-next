package settings

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeStateTransaction(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "theme.db")
	d, close, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: dsn}})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if err = data.ApplyMigrations(ctx, d.DB, db.SQLite, dsn); err != nil {
		t.Fatal(err)
	}
	r := NewRepoImpl(d)
	if err = r.PutThemeState(ctx, "0:classic", "", json.RawMessage(`{"revision":"one"}`), []port.Item{{Group: "template", Key: activeThemeKey, Value: json.RawMessage(`{"key":"classic"}`)}}); err != nil {
		t.Fatal(err)
	}
	if err = r.PutThemeState(ctx, "0:classic", "", json.RawMessage(`{"revision":"lost"}`), nil); err == nil {
		t.Fatal("stale write accepted")
	}
	// An invalid companion setting must roll back both publication and activation.
	err = r.PutThemeState(ctx, "0:classic", "one", json.RawMessage(`{"revision":"two"}`), []port.Item{
		{Group: "template", Key: activeThemeKey, Value: json.RawMessage(`{"key":"other"}`)},
		{Group: "template", Key: strings.Repeat("x", 101), Value: json.RawMessage(`null`)},
	})
	if err == nil {
		t.Fatal("invalid companion setting accepted")
	}
	raw, _ := r.Get(ctx, themeStateGroup, "0:classic")
	if string(raw) != `{"revision":"one"}` {
		t.Fatal("partial publication", string(raw))
	}
	active, _ := r.Get(ctx, "template", activeThemeKey)
	if string(active) != `{"key":"classic"}` {
		t.Fatal("partial activation", string(active))
	}
	// Saving a draft then publishing must update an existing state row.
	if err = r.PutThemeState(ctx, "0:classic", "one", json.RawMessage(`{"revision":"two"}`), nil); err != nil {
		t.Fatal("updating existing theme settings failed", err)
	}
	raw, _ = r.Get(ctx, themeStateGroup, "0:classic")
	if string(raw) != `{"revision":"two"}` {
		t.Fatal("updated settings not persisted", string(raw))
	}
	if err = r.PutThemeState(ctx, "0:classic", "one", json.RawMessage(`{"revision":"stale"}`), nil); err == nil {
		t.Fatal("stale update replaced published settings")
	}
	raw, _ = r.Get(ctx, themeStateGroup, "0:classic")
	if string(raw) != `{"revision":"two"}` {
		t.Fatal("stale update changed published settings", string(raw))
	}
}
