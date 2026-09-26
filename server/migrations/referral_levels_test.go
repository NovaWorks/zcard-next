package migrations_test

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferralUpgradePreservesUsers(t *testing.T) {
	d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(t.TempDir(), "upgrade.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	d.DB.SetMaxOpenConns(1)
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, name := range files {
		sql, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "_referral_member_levels.sql") {
			upgrade = sql
			break
		}
		if _, err = d.DB.Exec(string(sql)); err != nil {
			t.Fatal(name, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("missing migration")
	}
	for _, sql := range []string{
		`INSERT INTO member_levels(id,created_at,updated_at,name,discount,acquire_mode) VALUES(12,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'old private',9000,'manual')`,
		`INSERT INTO users(id,created_at,updated_at,username,password_hash,promo_code,manual_level_id,invite_l1) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'old-user','old-hash','ABCDEFGH',12,7)`,
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,user_id) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'OLD',900,42)`,
	} {
		if _, err = d.DB.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.DB.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	u := d.Client.User.GetX(context.Background(), 42)
	if u.Username != "old-user" || u.PasswordHash != "old-hash" || u.ManualLevelID != 12 || u.InviteL1 != 7 || u.PromoCode != "ABCDEFGH" || u.ReferralLevelID != 0 || u.InviteLevelID != 0 {
		t.Fatal("old account changed", u)
	}
	if o := d.Client.Order.GetX(context.Background(), 43); o.TotalAmount != 900 || o.UserID != 42 {
		t.Fatal("old order changed")
	}
	if _, err = d.Client.User.Create().SetUsername("old-user").Save(context.Background()); err == nil {
		t.Fatal("unique constraint lost")
	}
}
