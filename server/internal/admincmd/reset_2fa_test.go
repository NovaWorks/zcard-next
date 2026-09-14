package admincmd

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"os"
	"path/filepath"
	"testing"
)

func TestReset2FAWithoutServerOrEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "recovery.db")
	cfg := fmt.Sprintf("data:\n  database:\n    driver: sqlite\n    source: %s\n", dbPath)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	d, close, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: dbPath}})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	ctx := context.Background()
	if err = d.Client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := d.Client.AdminUser.Create().SetUsername("locked-admin").SetPasswordHash("preserve-password-hash").SetRoleID(42).SetEnabled(false).SetTotpSecret([]byte("undecryptable-old-key")).SetMfaState(`{"pending":"YWJj","recovery":["old-hash"],"factor_fails":5,"factor_until":9999999999}`).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--conf", dir, "--username", u.Username}
	if err = runReset2FA(append(args, "--dry-run")); err != nil {
		t.Fatal(err)
	}
	current, err := d.Client.AdminUser.Get(ctx, u.ID)
	if err != nil || len(current.TotpSecret) == 0 {
		t.Fatal("dry run mutated credentials", err)
	}
	if err = runReset2FA(append(args, "--yes")); err != nil {
		t.Fatal(err)
	}
	current, err = d.Client.AdminUser.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Enabled || current.PasswordHash != u.PasswordHash || current.RoleID != 42 || len(current.TotpSecret) != 0 || current.AuthVersion != 1 || current.MfaState != "{}" {
		t.Fatalf("wrong recovery state: enabled=%v version=%d", current.Enabled, current.AuthVersion)
	}
	if err = runReset2FA(append(args, "--yes")); err != nil {
		t.Fatal("repeat recovery", err)
	}
	if err = runReset2FA([]string{"--conf", dir, "--username", "missing", "--yes"}); err == nil {
		t.Fatal("unknown account succeeded")
	}
	if n, err := d.Client.SecurityAuditLog.Query().Count(ctx); err != nil || n != 2 {
		t.Fatal("missing audit", n, err)
	}
}
func TestReset2FARefusesNewSQLite(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(fmt.Sprintf("data:\n  database:\n    driver: sqlite\n    source: %s\n", missing)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runReset2FA([]string{"--conf", dir, "--username", "admin", "--yes"}); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("created an empty database")
	}
}
