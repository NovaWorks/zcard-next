package migrations_test

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefundIdempotencyUpgradePreservesHistory(t *testing.T) {
	h, e := db.SQLite.Open(filepath.Join(t.TempDir(), "refund.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, f := range files {
		b, e := fs.ReadFile(dir, f)
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasSuffix(f, "_refund_request_idempotency.sql") {
			upgrade = b
			break
		}
		if _, e = h.Exec(string(b)); e != nil {
			t.Fatal(f, e)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("migration missing")
	}
	for _, q := range []string{
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status) VALUES(1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'REFUND-HISTORY',1000,'refunded')`,
		`INSERT INTO refund_orders(id,created_at,updated_at,order_id,amount,shipping_amount,fee_amount,channel,status,item_allocations,upstream_refund_id,reason) VALUES(7,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,1,600,100,10,'gateway','succeeded','[{"item_id":3,"cancel_quantity":1}]','RECEIPT-7','legacy')`,
		`INSERT INTO refund_orders(id,created_at,updated_at,order_id,amount,channel,status) VALUES(8,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,1,400,'wallet','succeeded')`,
	} {
		if _, e = h.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = h.Exec(string(upgrade)); e != nil {
		t.Fatal(e)
	}
	var amount, shipping, fee int
	var allocations, receipt, reason string
	if e = h.QueryRow(`SELECT amount,shipping_amount,fee_amount,item_allocations,upstream_refund_id,reason FROM refund_orders WHERE id=7`).Scan(&amount, &shipping, &fee, &allocations, &receipt, &reason); e != nil {
		t.Fatal(e)
	}
	if amount != 600 || shipping != 100 || fee != 10 || receipt != "RECEIPT-7" || reason != "legacy" || !strings.Contains(allocations, `"cancel_quantity":1`) {
		t.Fatal("refund history changed")
	}
	var n int
	if e = h.QueryRow(`SELECT COUNT(*) FROM refund_orders WHERE request_key IS NULL`).Scan(&n); e != nil || n != 2 {
		t.Fatal("legacy null keys not preserved", n, e)
	}
	if _, e = h.Exec(`UPDATE refund_orders SET request_key='same-key' WHERE id=7`); e != nil {
		t.Fatal(e)
	}
	if _, e = h.Exec(`UPDATE refund_orders SET request_key='same-key' WHERE id=8`); e == nil {
		t.Fatal("duplicate key accepted")
	}
}
