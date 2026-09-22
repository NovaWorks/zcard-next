package migrations_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestBepusdtAttemptUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_BE_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
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
			for _, file := range files {
				b, err := fs.ReadFile(dir, file)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(file, "_bepusdt_attempt.sql") {
					upgrade = b
					break
				}
				if _, err := d.DB.Exec(string(b)); err != nil {
					t.Fatal(file, err)
				}
			}
			if len(upgrade) == 0 || strings.Contains(string(upgrade), "DROP TABLE") {
				t.Fatal("expected additive attempt migration")
			}
			_, err = d.DB.Exec(`INSERT INTO payments(id,created_at,updated_at,channel,channel_order_no,amount,charged_amount,status) VALUES(41,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy','old-trade',1000,1000,'success'),(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy',NULL,2000,0,'pending')`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.DB.Exec(string(upgrade)); err != nil {
				t.Fatal(err)
			}
			p, err := d.Client.Payment.Query().Where(payment.ID(41)).Select(payment.FieldID, payment.FieldAmount, payment.FieldChargedAmount, payment.FieldStatus, payment.FieldChannelOrderNo, payment.FieldGatewayOrderRef, payment.FieldGatewayContext).Only(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if p.Amount != 1000 || p.ChargedAmount != 1000 || p.Status != "success" || p.ChannelOrderNo != "old-trade" || p.GatewayOrderRef != "" || len(p.GatewayContext) != 0 {
				t.Fatal("legacy payment changed")
			}
			var ref sql.NullString
			if err := d.DB.QueryRow("SELECT gateway_order_ref FROM payments WHERE id=42").Scan(&ref); err != nil || ref.Valid {
				t.Fatal("legacy ref must be NULL", err)
			}
			err = d.Client.Payment.Update().Where(payment.ID(41)).SetGatewayOrderRef("BE-unique").SetGatewayContext([]byte(`{"lease":"test"}`)).Exec(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = d.Client.Payment.Update().Where(payment.ID(42)).SetGatewayOrderRef("BE-unique").Exec(context.Background()); err == nil {
				t.Fatal("reference unique index missing")
			}
		})
	}
}
