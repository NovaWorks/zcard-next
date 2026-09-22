package migrations_test

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaymentCheckoutUpgradePreservesAmounts(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_CHECKOUT_" + strings.ToUpper(driver) + "_DSN")
			if driver == "sqlite" {
				source = filepath.Join(t.TempDir(), "checkout.db")
			}
			if source == "" {
				t.Skip("requires disposable upgrade database")
			}
			d, cleanup, e := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if e != nil {
				t.Fatal(e)
			}
			defer cleanup()
			d.DB.SetMaxOpenConns(1)
			dir, _ := migrations.FS(driver)
			files, _ := fs.Glob(dir, "*.sql")
			found := false
			for _, name := range files {
				sql, e := fs.ReadFile(dir, name)
				if e != nil {
					t.Fatal(e)
				}
				if strings.HasSuffix(name, "_payment_checkout_fees.sql") {
					found = true
					for _, statement := range []string{
						`INSERT INTO payment_channels(id,created_at,updated_at,name,code,driver,config,fee,fee_type,fee_bearer,sort) VALUES(41,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'Legacy','legacy','epay','ciphertext',50,'percent','merchant',23)`,
						`INSERT INTO payments(id,created_at,updated_at,channel,amount,charged_amount,status) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy',1000,1000,'success')`,
						`INSERT INTO orders(id,created_at,updated_at,order_no,status,total_amount) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'fee-upgrade','paid',1000)`,
						`INSERT INTO refund_orders(id,created_at,updated_at,order_id,amount,channel,status) VALUES(44,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,43,100,'wallet','succeeded')`,
					} {
						if _, e = d.DB.Exec(statement); e != nil {
							t.Fatal(e)
						}
					}
				}
				if _, e = d.DB.Exec(string(sql)); e != nil {
					t.Fatal(name, e)
				}
			}
			if !found {
				t.Fatal("migration missing")
			}
			ctx := context.Background()
			ch := d.Client.PaymentChannel.GetX(ctx, 41)
			p := d.Client.Payment.GetX(ctx, 42)
			rf := d.Client.RefundOrder.GetX(ctx, 44)
			if ch.Fee != 50 || ch.FeeType != "percent" || ch.FeeBearer != "merchant" || ch.Sort != 23 || ch.Recommended || ch.RecommendLabel != "" || string(ch.Config) != "ciphertext" {
				t.Fatal("legacy channel changed")
			}
			if p.Amount != 1000 || p.ChargedAmount != 1000 || p.Fee != 0 || len(p.PricingSnapshot) != 0 {
				t.Fatal("legacy payment changed")
			}
			if rf.Amount != 100 || rf.FeeAmount != 0 {
				t.Fatal("legacy refund changed")
			}
			d.Client.PaymentChannel.UpdateOne(ch).SetRecommended(true).SetRecommendLabel("推荐").ExecX(ctx)
			d.Client.Payment.UpdateOne(p).SetPricingSnapshot([]byte(`{"base":1000,"fee":0,"total":1000}`)).ExecX(ctx)
		})
	}
}
