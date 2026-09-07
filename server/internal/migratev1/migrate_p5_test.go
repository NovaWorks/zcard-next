package migratev1

// P5 资金域全链路单测：bills 重放（方向/balance_before 推算/幂等键）、
// 余额对账差异暴露（注入不一致应被抓到）、withdrawals 状态与 method 合成、
// commissions 三态映射。

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newV1MoneySource(t *testing.T) *Source {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:migv1money%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", testDBSeq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	execScript(t, handle, []string{
		`CREATE TABLE settings (id INTEGER PRIMARY KEY, key TEXT, value TEXT, "group" TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, name TEXT, email TEXT, phone TEXT, password TEXT, status INTEGER, deleted_at TEXT, balance INTEGER, points INTEGER, pid INTEGER, group_id INTEGER, last_login_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE user_groups (id INTEGER PRIMARY KEY, name TEXT, discount TEXT, min_recharge INTEGER, min_consumption INTEGER, sort INTEGER, status INTEGER)`,
		`CREATE TABLE currencies (id INTEGER PRIMARY KEY, code TEXT, name TEXT, symbol TEXT, symbol_position TEXT, decimal_places INTEGER, exchange_rate TEXT, is_base INTEGER, is_enabled INTEGER, sort INTEGER)`,
		`CREATE TABLE supply_sources (id INTEGER PRIMARY KEY, name TEXT, driver TEXT, base_url TEXT, credentials TEXT, status TEXT, settings TEXT, balance_cache INTEGER, last_synced_at TEXT, last_error TEXT, deleted_at TEXT)`,
		`CREATE TABLE roles (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE model_has_roles (role_id INTEGER, model_type TEXT, model_id INTEGER)`,
		`CREATE TABLE categories (id INTEGER PRIMARY KEY, merchant_id INTEGER, parent_id INTEGER, name TEXT, icon TEXT, sort INTEGER, status INTEGER, hide INTEGER)`,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, merchant_id INTEGER, category_id INTEGER, name TEXT, slug TEXT, description TEXT, cover TEXT, images TEXT,
			price INTEGER, factory_price INTEGER, draft_premium INTEGER, member_price TEXT, stock_type TEXT, fulfillment_type TEXT, delivery_message TEXT,
			stock_visible INTEGER, control_config TEXT, delivery_mode TEXT, dedup INTEGER, sort INTEGER, is_featured INTEGER, status INTEGER, hide INTEGER,
			upstream_source_id INTEGER, upstream_product_code TEXT, upstream_synced_at TEXT, virtual_reviews TEXT, deleted_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, order_no TEXT, merchant_id INTEGER, user_id INTEGER, product_id INTEGER, quantity INTEGER,
			amount INTEGER, discount_amount INTEGER, cost INTEGER, coupon_code TEXT, base_currency TEXT, display_currency TEXT,
			exchange_rate REAL, amount_display INTEGER, status TEXT, delivery_status TEXT,
			fulfillment_type_snapshot TEXT, payment_channel TEXT, subsite_id INTEGER, subsite_domain TEXT,
			subsite_profit INTEGER, source TEXT, upstream_order_id TEXT, sku_name TEXT,
			instructions_snapshot TEXT, delivery_message_snapshot TEXT, extra TEXT, contact TEXT,
			create_device TEXT, create_ip TEXT, paid_at TEXT, closed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE bills (id INTEGER PRIMARY KEY, user_id INTEGER, amount INTEGER, balance_after INTEGER, type INTEGER, log TEXT, order_id INTEGER, admin_id INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE withdrawals (id INTEGER PRIMARY KEY, user_id INTEGER, amount INTEGER, actual_amount INTEGER, fee INTEGER, method TEXT,
			account TEXT, account_name TEXT, status TEXT, reject_reason TEXT, admin_id INTEGER, processed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE commissions (id INTEGER PRIMARY KEY, order_id INTEGER, buyer_id INTEGER, referrer_id INTEGER, tier INTEGER, rate REAL,
			base_amount INTEGER, amount INTEGER, status TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE product_skus (id INTEGER PRIMARY KEY, product_id INTEGER, name TEXT, price INTEGER, upstream_sku_code TEXT, sort INTEGER, status INTEGER)`,
		`CREATE TABLE reviews (id INTEGER PRIMARY KEY, product_id INTEGER, user_id INTEGER, order_id INTEGER, rating INTEGER, content TEXT, status TEXT, created_at TEXT)`,
		`CREATE TABLE card_imports (id INTEGER PRIMARY KEY, product_id INTEGER, operator_id INTEGER, source TEXT, total INTEGER, success_count INTEGER, failed_count INTEGER, skipped_count INTEGER, status TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE cards (id INTEGER PRIMARY KEY, product_id INTEGER, import_id INTEGER, order_id INTEGER, content TEXT, content_hash TEXT, status TEXT,
			locked_at TEXT, used_at TEXT, created_at TEXT, updated_at TEXT, note TEXT, card_type TEXT, owner_id INTEGER,
			draft_premium INTEGER, draft_cost INTEGER, price INTEGER, number_hash TEXT)`,
		`CREATE TABLE order_items (id INTEGER PRIMARY KEY, order_id INTEGER, product_id INTEGER, amount INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE payments (id INTEGER PRIMARY KEY, order_id INTEGER, order_ids TEXT, recharge_id INTEGER, channel TEXT, channel_order_no TEXT,
			amount INTEGER, fee INTEGER, status TEXT, charged_currency TEXT, charged_amount INTEGER,
			channel_exchange_rate REAL, raw TEXT, paid_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE recharges (id INTEGER PRIMARY KEY, recharge_no TEXT, user_id INTEGER, amount INTEGER, status TEXT, target TEXT, paid_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE coupons (id INTEGER PRIMARY KEY, code TEXT, type TEXT, value INTEGER, product_id INTEGER, category_id INTEGER, min_amount INTEGER,
			status TEXT, expires_at TEXT, used_at TEXT, used_by INTEGER, order_id INTEGER, note TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE order_deliveries (id INTEGER PRIMARY KEY, order_id INTEGER, product_id INTEGER, card_content TEXT, delivered_mode TEXT, delivered_at TEXT)`,
	})
	execScript(t, handle, []string{
		`INSERT INTO settings VALUES (1,'site_name','"资金域测试"','storefront')`,
		`INSERT INTO user_groups VALUES (1,'普通会员','100.00',0,0,0,1)`,
		`INSERT INTO currencies VALUES (1,'CNY','人民币','¥','before',2,'1',1,1,0)`,
		// alice 余额 8000（快照）；bob 余额 500（末条流水对不上 → 差异清单）
		`INSERT INTO users VALUES (1,'alice','','a@x.com','','x',1,NULL,8000,0,0,1,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO users VALUES (2,'bob','','b@x.com','','x',1,NULL,500,0,1,1,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO products VALUES (1,1,NULL,'月卡','yueka','',NULL,NULL,1500,800,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO orders VALUES (1,'ORD-M1',1,1,1,1,1500,0,800,NULL,'CNY',NULL,NULL,NULL,'paid','delivered','auto_card',NULL,NULL,NULL,0,NULL,NULL,NULL,NULL,NULL,NULL,'a@x.com',NULL,NULL,'2026-02-01 10:00:00',NULL,'2026-02-01 10:00:00','2026-02-01 10:00:00')`,
	})
	execScript(t, handle, []string{
		// alice 流水链：+10000 → -2000 → 末条 8000（与快照一致 ✓）
		`INSERT INTO bills VALUES (1,1,10000,10000,1,'充值',NULL,NULL,'2026-01-05 10:00:00','2026-01-05 10:00:00')`,
		`INSERT INTO bills VALUES (2,1,2000,8000,0,'消费',1,NULL,'2026-02-01 10:00:00','2026-02-01 10:00:00')`,
		// bob 流水：+500 → 末条 500，但快照 balance=500 ✓；再加一条 +300 使末条 800 ≠ 快照 500（差异）
		`INSERT INTO bills VALUES (3,2,500,500,1,'充值',NULL,NULL,'2026-01-06 10:00:00','2026-01-06 10:00:00')`,
		`INSERT INTO bills VALUES (4,2,300,800,1,'活动赠送',NULL,NULL,'2026-01-07 10:00:00','2026-01-07 10:00:00')`,
		// 提现：pending / approved(已打款) / rejected
		`INSERT INTO withdrawals VALUES (1,1,1000,990,10,'alipay','ali@x.com','张三','pending',NULL,NULL,NULL,'2026-03-01 10:00:00','2026-03-01 10:00:00')`,
		`INSERT INTO withdrawals VALUES (2,1,2000,1980,20,'usdt','TX123','李四','approved',NULL,NULL,'2026-03-02 10:00:00','2026-03-02 10:00:00','2026-03-02 10:00:00')`,
		`INSERT INTO withdrawals VALUES (3,2,500,500,0,'wechat','wx@x.com','王五','rejected','账号异常',NULL,NULL,'2026-03-03 10:00:00','2026-03-03 10:00:00')`,
		// 佣金：available / pending / paid
		`INSERT INTO commissions VALUES (1,1,1,2,1,10.0,1500,150,'available','2026-02-01 10:00:00','2026-02-01 10:00:00')`,
		`INSERT INTO commissions VALUES (2,1,1,2,2,5.0,1500,75,'pending','2026-02-01 10:00:00','2026-02-01 10:00:00')`,
		`INSERT INTO commissions VALUES (3,1,1,2,3,2.5,1500,37,'paid','2026-02-01 10:00:00','2026-02-01 10:00:00')`,
	})
	return &Source{DB: handle}
}

func TestMigrateP5Money(t *testing.T) {
	src := newV1MoneySource(t)
	client := newTestClient(t)
	rw, _ := NewReportWriter(t.TempDir())
	t.Cleanup(func() { _ = rw.Close() })
	m := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m.DataKey = deriveTestKey(t, "dk")
	m.NewCardKey = deriveTestKey(t, "ck")

	ctx := context.Background()
	for _, step := range []func(context.Context) error{
		m.MigrateSystem, m.MigrateIdentity, m.MigrateCatalog, m.MigrateInventory, m.MigrateTrade, m.MigrateMoney,
	} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// bills：4 条全迁，方向与 before 推算正确
	bts, _ := client.WalletTransaction.Query().Order(ent.Asc(wallettransaction.FieldID)).All(ctx)
	if len(bts) != 4 {
		t.Fatalf("bills 期望 4 条，实际 %d", len(bts))
	}
	if bts[0].Direction != "in" || bts[0].BalanceBefore != 0 || bts[0].BalanceAfter != 10000 {
		t.Fatalf("充值流水异常: %+v", bts[0])
	}
	if bts[1].Direction != "out" || bts[1].BalanceBefore != 10000 || bts[1].BalanceAfter != 8000 {
		t.Fatalf("消费流水异常: %+v", bts[1])
	}
	if bts[1].OrderID == 0 || bts[1].Remark != "消费" {
		t.Fatalf("流水关联异常: %+v", bts[1])
	}
	if bts[0].Reference != "v1_import:bills:1" {
		t.Fatalf("幂等键异常: %s", bts[0].Reference)
	}

	// 对账：bob 差异被抓到（末条 800 ≠ 快照 500），alice 一致
	rc := m.Stats().Tables["wallet_reconcile"]
	if rc == nil || rc.SkippedExists != 1 || rc.Migrated != 1 {
		t.Fatalf("对账统计异常: %+v", rc)
	}

	// withdrawals：三态 + method JSON
	ws, _ := client.Withdrawal.Query().All(ctx)
	if len(ws) != 3 {
		t.Fatalf("withdrawals 期望 3，实际 %d", len(ws))
	}
	for _, w := range ws {
		switch w.Amount {
		case 1000:
			if w.Status != withdrawal.StatusPending || w.Method["channel"] != "alipay" || w.Method["name"] != "张三" {
				t.Fatalf("pending 提现异常: %+v", w)
			}
		case 2000:
			if w.Status != "paid" || w.PaidAt.IsZero() {
				t.Fatalf("approved→paid 异常: %+v", w)
			}
		case 500:
			if w.Status != "rejected" || w.RejectReason != "账号异常" {
				t.Fatalf("rejected 异常: %+v", w)
			}
		}
	}

	// commissions：三态映射 + 订单关联
	acs, _ := client.AffiliateCommission.Query().All(ctx)
	if len(acs) != 3 {
		t.Fatalf("commissions 期望 3，实际 %d", len(acs))
	}
	byTier := map[int8]*ent.AffiliateCommission{}
	for _, c := range acs {
		byTier[c.Tier] = c
	}
	if byTier[1] == nil || byTier[1].Status != affiliatecommission.StatusAvailable || byTier[1].OrderID == 0 || byTier[1].AvailableAt.IsZero() {
		t.Fatalf("available 佣金异常: %+v", byTier[1])
	}
	if byTier[2] == nil || byTier[2].Status != "pending_confirm" || !byTier[2].AvailableAt.IsZero() {
		t.Fatalf("pending→pending_confirm 异常: %+v", byTier[2])
	}
	if byTier[3] == nil || byTier[3].Status != "withdrawn" {
		t.Fatalf("paid→withdrawn 异常: %+v", byTier[3])
	}

	// 幂等重跑：reference 判据全部跳过
	m2 := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m2.DataKey, m2.NewCardKey = m.DataKey, m.NewCardKey
	if err := m2.MigrateMoney(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m2.Stats().Tables["bills"]; got.Migrated != 0 || got.SkippedExists != 4 {
		t.Fatalf("bills 重跑应全部幂等跳过: %+v", got)
	}
	n, _ := client.WalletTransaction.Query().Count(ctx)
	if n != 4 {
		t.Fatalf("重跑后流水重复: %d", n)
	}
}
