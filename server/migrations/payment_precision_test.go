package migrations_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestPaymentPrecisionUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_MONEY_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
			if driver == "sqlite" {
				source = filepath.Join(t.TempDir(), "upgrade.db")
			}
			if source == "" {
				t.Skip("isolated upgrade database not configured")
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			dir, _ := migrations.FS(driver)
			files, _ := fs.Glob(dir, "*.sql")
			var upgrade []byte
			for _, file := range files {
				b, err := fs.ReadFile(dir, file)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(file, "_payment_charge_precision.sql") {
					upgrade = b
					break
				}
				if _, err := d.DB.Exec(string(b)); err != nil {
					t.Fatal(file, err)
				}
			}
			if len(upgrade) == 0 {
				t.Fatal("missing migration")
			}
			_, err = d.DB.Exec(`INSERT INTO payments (id,created_at,updated_at,channel,amount,charged_amount,charged_units,charged_currency,exchange_rate,status) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy',200,200,28,'USD',0.14,'success')`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.DB.Exec(string(upgrade)); err != nil {
				t.Fatal(err)
			}
			row, err := d.Client.Payment.Get(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if row.Amount != 200 || row.ChargedAmount != 200 || row.ChargedUnits != 28 || row.ChargedCurrency != "USD" || row.ExchangeRate != 0.14 || row.ChargedPrecision != -1 {
				t.Fatalf("migration altered money: %+v", row)
			}
		})
	}
}
