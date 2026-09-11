package settings

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"path/filepath"
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
	err = r.PutThemeState(ctx, "0:classic", "one", json.RawMessage(`{"revision":"two"}`), []port.Item{{Group: "template", Key: "", Value: json.RawMessage(`null`)}})
	if err == nil {
		t.Fatal("invalid companion setting accepted")
	}
	raw, _ := r.Get(ctx, themeStateGroup, "0:classic")
	if string(raw) != `{"revision":"one"}` {
		t.Fatal("partial publication", string(raw))
	}
}
