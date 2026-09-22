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

func TestPaymentChannelDeleteUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_CHANNEL_DELETE_" + strings.ToUpper(driver) + "_DSN")
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
				if strings.HasSuffix(file, "_payment_channel_delete.sql") {
					upgrade = b
					break
				}
				if _, err := d.DB.Exec(string(b)); err != nil {
					t.Fatal(file, err)
				}
			}
			if len(upgrade) == 0 {
				t.Fatal("channel delete migration missing")
			}
			if _, err := d.DB.Exec(`INSERT INTO payment_channels(id,created_at,updated_at,name,code,driver,config,enabled) VALUES(41,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'Old BEpusdt','be-old','bepusdt','ciphertext',false)`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.DB.Exec(`INSERT INTO payments(id,created_at,updated_at,channel_id,channel,driver_snapshot,amount,status) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,'be-old','bepusdt',1000,'pending')`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.DB.Exec(string(upgrade)); err != nil {
				t.Fatal(err)
			}
			ch := d.Client.PaymentChannel.GetX(context.Background(), 41)
			if !ch.DeletedAt.IsZero() || ch.Enabled || ch.Code != "be-old" || string(ch.Config) != "ciphertext" {
				t.Fatal("upgrade changed historical channel or credentials")
			}
			p := d.Client.Payment.GetX(context.Background(), 42)
			if p.ChannelID != ch.ID || p.Amount != 1000 || p.Status != "pending" {
				t.Fatal("upgrade changed historical payment")
			}
		})
	}
}
