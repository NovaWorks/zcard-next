package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

func TestTelegramTokenEncryptedMaskedAndPreserved(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "telegram.db")
	d, close, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: dsn}})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if err = data.ApplyMigrations(ctx, d.DB, db.SQLite, dsn); err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.NewBox(bytes.Repeat([]byte{3}, 32))
	repo := ProvideRepo(d, box)
	uc := NewSettingsUsecase(repo)
	svc := NewAdminSettingsService(uc)
	token := "123456:abcdefghijklmnopqrstuvwxyz"
	raw, _ := json.Marshal(token)
	reply, err := svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "notify", Key: "telegram_bot_token", ValueJson: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if reply.ValueJson != `"****"` {
		t.Fatal("write echoed secret")
	}
	stored, err := d.Client.Setting.Query().Where(setting.Group("notify"), setting.Key("telegram_bot_token")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Value), token) || !strings.Contains(string(stored.Value), "ciphertext") {
		t.Fatal("plaintext storage")
	}
	for _, v := range []string{`""`, `"****"`} {
		if err = uc.PutMany(ctx, []port.Item{{Group: "notify", Key: "telegram_bot_token", Value: json.RawMessage(v)}}); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Get(ctx, "notify", "telegram_bot_token")
		if err != nil || string(got) != string(raw) {
			t.Fatal("secret not preserved", err)
		}
	}
	list, err := svc.ListSettings(ctx, &adminv1.ListSettingsRequest{Group: "notify"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), token) || strings.Contains(string(b), "ciphertext") {
		t.Fatal("secret exposed in settings")
	}
	badbox, _ := crypto.NewBox(bytes.Repeat([]byte{4}, 32))
	if _, err = ProvideRepo(d, badbox).Get(ctx, "notify", "telegram_bot_token"); err == nil {
		t.Fatal("bad key accepted")
	}
}
