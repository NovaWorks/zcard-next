package migrations_test

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminMFAUpgradePreservesExistingBinding(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, f := range files {
		b, err := fs.ReadFile(dir, f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(f, "_admin_two_factor.sql") {
			upgrade = b
			break
		}
		if _, err = h.Exec(string(b)); err != nil {
			t.Fatal(f, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("missing migration")
	}
	_, err = h.Exec(`INSERT INTO admin_users (id,created_at,updated_at,username,password_hash,role_id,totp_secret,enabled) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'existing','old-hash',1,x'01020304',1)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var enc []byte
	var state string
	var version int
	if err = h.QueryRow("SELECT totp_secret,mfa_state,auth_version FROM admin_users WHERE id=1").Scan(&enc, &state, &version); err != nil {
		t.Fatal(err)
	}
	if string(enc) != string([]byte{1, 2, 3, 4}) || state != "{}" || version != 0 {
		t.Fatal("upgrade altered existing credentials")
	}
}

func TestAdminMFAUpgradeExternal(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_MFA_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
			if source == "" {
				t.Skip("isolated upgrade DB not configured")
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			dir, _ := migrations.FS(driver)
			files, _ := fs.Glob(dir, "*.sql")
			var upgrade []byte
			for _, f := range files {
				b, err := fs.ReadFile(dir, f)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(f, "_admin_two_factor.sql") {
					upgrade = b
					break
				}
				if _, err = d.DB.Exec(string(b)); err != nil {
					t.Fatal(f, err)
				}
			}
			if len(upgrade) == 0 {
				t.Fatal("missing migration")
			}
			placeholder := "?"
			if driver == "postgres" {
				placeholder = "$1"
			}
			_, err = d.DB.Exec("INSERT INTO admin_users (id,created_at,updated_at,username,password_hash,role_id,totp_secret,enabled) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'existing','old-hash',1,"+placeholder+",true)", []byte{1, 2, 3, 4})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.DB.Exec(string(upgrade)); err != nil {
				t.Fatal(err)
			}
			row, err := d.Client.AdminUser.Get(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if string(row.TotpSecret) != string([]byte{1, 2, 3, 4}) || row.MfaState != "{}" || row.AuthVersion != 0 {
				t.Fatal("upgrade corrupted binding or omitted state defaults")
			}
		})
	}
}
