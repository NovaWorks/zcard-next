package migratev1

// P4 交易域全链路单测：sqlite 源 + ent sqlite 目标，跑 P0→P4。
// 覆盖：订单状态映射（附录A）、order_no 保留、行式金额恒等式（SUM==total）、
// 状态事件回填、聚合支付首单+raw、充值状态映射、优惠券换算与 scope、
// cards.order_id 回填、分站订单跳过、幂等重跑。

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newV1TradeSource(t *testing.T) *Source {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:migv1trade%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", testDBSeq.Add(1)))
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
		`CREATE TABLE product_skus (id INTEGER PRIMARY KEY, product_id INTEGER, name TEXT, price INTEGER, upstream_sku_code TEXT, sort INTEGER, status INTEGER)`,
		`CREATE TABLE reviews (id INTEGER PRIMARY KEY, product_id INTEGER, user_id INTEGER, order_id INTEGER, rating INTEGER, content TEXT, status TEXT, created_at TEXT)`,
		`CREATE TABLE card_imports (id INTEGER PRIMARY KEY, product_id INTEGER, operator_id INTEGER, source TEXT, total INTEGER, success_count INTEGER, failed_count INTEGER, skipped_count INTEGER, status TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE cards (id INTEGER PRIMARY KEY, product_id INTEGER, import_id INTEGER, order_id INTEGER, content TEXT, content_hash TEXT, status TEXT,
			locked_at TEXT, used_at TEXT, created_at TEXT, updated_at TEXT, note TEXT, card_type TEXT, owner_id INTEGER,
			draft_premium INTEGER, draft_cost INTEGER, price INTEGER, number_hash TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, order_no TEXT, merchant_id INTEGER, user_id INTEGER, product_id INTEGER, quantity INTEGER,
			amount INTEGER, discount_amount INTEGER, cost INTEGER, coupon_code TEXT, base_currency TEXT, display_currency TEXT,
			exchange_rate REAL, amount_display INTEGER, status TEXT, delivery_status TEXT,
			fulfillment_type_snapshot TEXT, payment_channel TEXT, subsite_id INTEGER, subsite_domain TEXT,
			subsite_profit INTEGER, source TEXT, upstream_order_id TEXT, sku_name TEXT,
			instructions_snapshot TEXT, delivery_message_snapshot TEXT, extra TEXT, contact TEXT,
			create_device TEXT, create_ip TEXT, paid_at TEXT, closed_at TEXT, created_at TEXT, updated_at TEXT)`,
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
		`INSERT INTO settings VALUES (1,'site_name','"交易域测试"','storefront')`,
		`INSERT INTO user_groups VALUES (1,'普通会员','100.00',0,0,0,1)`,
		`INSERT INTO currencies VALUES (1,'CNY','人民币','¥','before',2,'1',1,1,0)`,
		`INSERT INTO users VALUES (1,'bob','','b@x.com','','x',1,NULL,0,0,0,1,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO products VALUES (1,1,NULL,'月卡','yueka','',NULL,NULL,1500,1000,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO products VALUES (2,2,NULL,'分站品','sub','',NULL,NULL,900,0,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
	})
	execScript(t, handle, []string{
		// o1 已支付已交付（券抵扣 + 分站利润）；o2 游客待付；o3 已关闭；o4 已退款；o5 分站
		`INSERT INTO orders VALUES (1,'E2E-ORD-A',1,1,1,1,1500,100,800,'SAVE10','CNY','CNY',1.0,1500,'paid','delivered','auto_card','alipay',NULL,NULL,200,NULL,NULL,'月卡',NULL,NULL,'{"query_password":"$2y$fake","access_token_hash":"abc"}','bob@x.com','win','1.2.3.4','2026-02-01 10:00:00',NULL,'2026-02-01 09:00:00','2026-02-01 10:00:00')`,
		`INSERT INTO orders VALUES (2,'E2E-ORD-B',1,NULL,1,2,3000,0,1600,NULL,'CNY','USD',0.14,4200,'pending','pending','auto_card',NULL,NULL,NULL,0,NULL,NULL,NULL,NULL,NULL,NULL,'guest@x.com',NULL,'5.6.7.8',NULL,NULL,'2026-02-02 10:00:00','2026-02-02 10:00:00')`,
		`INSERT INTO orders VALUES (3,'E2E-ORD-C',1,1,1,1,1500,0,800,NULL,'CNY',NULL,NULL,NULL,'closed','pending','auto_card',NULL,NULL,NULL,0,NULL,NULL,NULL,NULL,NULL,NULL,'bob@x.com',NULL,'1.2.3.4',NULL,'2026-02-03 11:00:00','2026-02-03 10:00:00','2026-02-03 11:00:00')`,
		`INSERT INTO orders VALUES (4,'E2E-ORD-D',1,1,1,1,1500,0,800,NULL,'CNY',NULL,NULL,NULL,'refunded','delivered','auto_card',NULL,NULL,NULL,0,NULL,NULL,NULL,NULL,NULL,NULL,'bob@x.com',NULL,'1.2.3.4','2026-02-04 10:00:00',NULL,'2026-02-04 09:00:00','2026-02-04 10:00:00')`,
		`INSERT INTO orders VALUES (5,'E2E-ORD-SUB',2,1,2,1,900,0,500,NULL,'CNY',NULL,NULL,NULL,'paid','delivered','auto_card',NULL,9,'sub.e2e.test',400,NULL,NULL,NULL,NULL,NULL,NULL,'x@y.com',NULL,'9.9.9.9','2026-02-05 10:00:00',NULL,'2026-02-05 09:00:00','2026-02-05 10:00:00')`,
		`INSERT INTO order_items VALUES (1,1,1,1500,'2026-02-01 09:00:00','2026-02-01 10:00:00')`,
		// 支付：p1 普通成功；p2 聚合（order_ids 两单）；p3 充值支付
		`INSERT INTO payments VALUES (1,1,NULL,NULL,'alipay','ALI20260201',1500,3,'success','CNY',1500,1.0,'{"trade_no":"ALI20260201"}','2026-02-01 10:00:00','2026-02-01 09:00:00','2026-02-01 10:00:00')`,
		`INSERT INTO payments VALUES (2,NULL,'[1,2]',NULL,'epay','EP99',4500,0,'success','CNY',4500,1.0,'{"agg":true}','2026-02-02 10:00:00','2026-02-02 09:00:00','2026-02-02 10:00:00')`,
		`INSERT INTO payments VALUES (3,NULL,NULL,1,'usdt','U1',5000,10,'success','USDT',700000,7.0,'{"tx":"0xabc"}','2026-02-06 10:00:00','2026-02-06 09:00:00','2026-02-06 10:00:00')`,
		`INSERT INTO recharges VALUES (1,'RCH-001',1,5000,'paid','balance','2026-02-06 10:00:00','2026-02-06 09:00:00','2026-02-06 10:00:00')`,
		// 优惠券：fixed（带门槛+商品）/ percent(10%→1000 万分比) / used
		`INSERT INTO coupons VALUES (1,'SAVE10','fixed',500,1,NULL,1000,'active','2027-01-01 00:00:00',NULL,NULL,NULL,'满10减5','2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO coupons VALUES (2,'PCT10','percent',10,NULL,NULL,0,'active','2027-01-01 00:00:00',NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO coupons VALUES (3,'USED01','fixed',300,NULL,NULL,0,'used','2027-01-01 00:00:00','2026-03-01 10:00:00',1,1,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		// 已售卡（o1）+ 交付记录（status 模式卡仍在 + delete 模式物理删）
		`INSERT INTO cards VALUES (1,1,NULL,1,'SOLD-CARD-001','` + sha256Hex("SOLD-CARD-001") + `','used',NULL,'2026-02-01 10:05:00','2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,0,NULL)`,
		`INSERT INTO cards VALUES (2,1,NULL,4,'REFUND-CARD-004','` + sha256Hex("REFUND-CARD-004") + `','used',NULL,'2026-02-04 10:05:00','2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,0,NULL)`,
		`INSERT INTO order_deliveries VALUES (1,1,1,'SOLD-CARD-001','status','2026-02-01 10:05:00')`,
		`INSERT INTO order_deliveries VALUES (2,4,1,'REFUND-CARD-004','status','2026-02-04 10:05:00')`,
	})
	return &Source{DB: handle}
}

func TestMigrateP4Trade(t *testing.T) {
	src := newV1TradeSource(t)
	client := newTestClient(t)
	rw, _ := NewReportWriter(t.TempDir())
	t.Cleanup(func() { _ = rw.Close() })
	m := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m.DataKey = deriveTestKey(t, "dk")
	m.NewCardKey = deriveTestKey(t, "ck2")

	ctx := context.Background()
	for _, step := range []func(context.Context) error{
		m.MigrateSystem, m.MigrateIdentity, m.MigrateCatalog, m.MigrateInventory, m.MigrateTrade,
	} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 订单：4 行（分站跳过），状态映射 + order_no 保留
	orders, _ := client.Order.Query().All(ctx)
	if len(orders) != 4 {
		t.Fatalf("orders 期望 4 行，实际 %d", len(orders))
	}
	byNo := map[string]*ent.Order{}
	for _, o := range orders {
		byNo[o.OrderNo] = o
	}
	o1 := byNo["E2E-ORD-A"]
	if o1 == nil || string(o1.Status) != "completed" || o1.TotalAmount != 1500 || o1.QueryPasswordHash != "$2y$fake" {
		t.Fatalf("o1 异常: %+v", o1)
	}
	if o1.PaidAt.IsZero() || o1.UserID == 0 || o1.Contact == "" {
		t.Fatalf("o1 关联字段异常: %+v", o1)
	}
	if o1.Extra["v1_coupon_code"] != "SAVE10" || o1.Extra["v1_create_device"] != "win" {
		t.Fatalf("o1 extra 保真异常: %v", o1.Extra)
	}
	if byNo["E2E-ORD-B"].Status != order.StatusPendingPayment || byNo["E2E-ORD-B"].GuestContact != "guest@x.com" {
		t.Fatal("o2 游客单异常")
	}
	if byNo["E2E-ORD-C"].Status != "canceled" || byNo["E2E-ORD-C"].ClosedAt.IsZero() {
		t.Fatal("o3 closed 映射异常")
	}
	if byNo["E2E-ORD-D"].Status != "refunded" {
		t.Fatal("o4 refunded 映射异常")
	}

	// 行式金额恒等式：SUM(lines) == total_amount（o1：base 1600 - 券 100 + 分站加价 200 = 1700？）
	// 注意：o1 amount=1500 discount=100 subsite_profit=200 → base=1600, coupon=-100, markup=+200 → SUM=1700 ≠ total=1500
	// ——恒等式要求 total=SUM：1.x 的 subsite_profit 是利润（售价含），不叠加到 total。
	// 修正断言按实现语义：lines = base(1600) + coupon(-100) = 1500 == total ✓（markup 行只对分站单）
	lines, _ := client.OrderAmountLine.Query().Where(orderamountline.OrderIDEQ(o1.ID)).All(ctx)
	var sum int64
	for _, l := range lines {
		sum += l.Amount
	}
	if sum != o1.TotalAmount {
		t.Fatalf("金额恒等式破坏: SUM(lines)=%d total=%d", sum, o1.TotalAmount)
	}

	// 状态事件：o1 有 created + paid 两个锚点
	evs, _ := client.OrderStatusEvent.Query().All(ctx)
	o1Events := 0
	for _, e := range evs {
		if e.OrderID == o1.ID {
			o1Events++
		}
	}
	if o1Events != 2 {
		t.Fatalf("o1 状态事件期望 2（created+paid），实际 %d", o1Events)
	}

	// 支付：3 行；聚合 p2 关联首单 o1；充值支付 p3 关联 recharge
	pays, _ := client.Payment.Query().All(ctx)
	if len(pays) != 3 {
		t.Fatalf("payments 期望 3 行，实际 %d", len(pays))
	}
	recharge, _ := client.RechargeOrder.Query().Only(ctx)
	if recharge == nil || string(recharge.Status) != "success" || recharge.Target != "balance" {
		t.Fatalf("recharge 异常: %+v", recharge)
	}
	var aggPay, rechargePay *ent.Payment
	for _, p := range pays {
		if p.ChannelOrderNo == "EP99" {
			aggPay = p
		}
		if p.Channel == "usdt" {
			rechargePay = p
		}
	}
	if aggPay == nil || aggPay.OrderID != o1.ID {
		t.Fatalf("聚合支付应关联首单 o1: %+v", aggPay)
	}
	if aggPay.ChargedUnits != 4500 || aggPay.ChargedAmount != 4500 {
		t.Fatalf("聚合支付金额异常: %+v", aggPay)
	}
	if rechargePay == nil || rechargePay.RechargeOrderID == 0 {
		t.Fatalf("充值支付未关联: %+v", rechargePay)
	}

	// 优惠券：换算与 scope、used 关联
	cs, _ := client.Coupon.Query().All(ctx)
	if len(cs) != 3 {
		t.Fatalf("coupons 期望 3，实际 %d", len(cs))
	}
	for _, c := range cs {
		switch c.Code {
		case "SAVE10":
			if c.Value != 500 || c.Type != "fixed" || fmt.Sprint(c.Scope["min_amount"]) != "1000" || c.Scope["product_ids"] == nil {
				t.Fatalf("SAVE10 异常: %+v", c)
			}
		case "PCT10":
			if c.Value != 1000 || c.Type != "percent" {
				t.Fatalf("PCT10 换算异常: %+v", c)
			}
		case "USED01":
			if string(c.Status) != "used" || c.UsedOrderID == 0 || c.UserID == 0 {
				t.Fatalf("USED01 异常: %+v", c)
			}
		}
	}

	// cards.order_id 回填：SOLD-CARD → o1
	sold, err := client.Card.Query().Where().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var soldCard *ent.Card
	for _, c := range sold {
		if c.ContentHash != "" && c.Status == "used" && c.OrderID == o1.ID {
			soldCard = c
		}
	}
	if soldCard == nil {
		t.Fatalf("已售卡 order_id 未回填到 o1(%d): %+v", o1.ID, sold)
	}
	// order_deliveries 迁移：2 行（status 模式卡在库）
	dvs, _ := client.OrderDelivery.Query().Count(ctx)
	if dvs != 2 {
		t.Fatalf("order_deliveries 期望 2，实际 %d", dvs)
	}

	// 幂等重跑
	m2 := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m2.DataKey, m2.NewCardKey = m.DataKey, m.NewCardKey
	if err := m2.MigrateTrade(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m2.Stats().Tables["orders"]; got.Migrated != 0 || got.SkippedExists != 5 {
		t.Fatalf("订单重跑应全部跳过（4 幂等 + 1 分站）: %+v", got)
	}
	n, _ := client.Order.Query().Count(ctx)
	if n != 4 {
		t.Fatalf("重跑后订单数异常: %d", n)
	}
	evs2, _ := client.OrderStatusEvent.Query().Count(ctx)
	if int(evs2) != len(evs) {
		t.Fatalf("重跑后事件重复: %d → %d", len(evs), evs2)
	}
}

func TestRunVerify(t *testing.T) {
	src := newV1MoneySource(t)
	client := newTestClient(t)
	rw, _ := NewReportWriter(t.TempDir())
	t.Cleanup(func() { _ = rw.Close() })
	m := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m.DataKey = deriveTestKey(t, "dk")
	m.NewCardKey = deriveTestKey(t, "ck2")

	ctx := context.Background()
	for _, step := range []func(context.Context) error{
		m.MigrateSystem, m.MigrateIdentity, m.MigrateCatalog, m.MigrateInventory, m.MigrateTrade, m.MigrateMoney,
	} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	res, err := m.RunVerify(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	// P5 源里 bob 的流水/快照差异是故意设计的 → verify 应如实报 fail
	if res.Passed {
		t.Fatal("存在已知的钱包差异，verify 不应直接通过")
	}
	walletFail := false
	for _, c := range res.Checks {
		if c.Name == "钱包对账" && c.Status == "fail" {
			walletFail = true
		}
	}
	if !walletFail {
		t.Fatal("钱包差异应被 verify 抓到")
	}
	// 修平差异（每账户快照对齐各自末条流水）→ 全部通过
	accs, _ := client.WalletAccount.Query().All(ctx)
	for _, a := range accs {
		last, err := client.WalletTransaction.Query().
			Where(wallettransaction.UserID(a.UserID)).
			Order(ent.Desc(wallettransaction.FieldID)).
			First(ctx)
		if err == nil {
			client.WalletAccount.UpdateOne(a).SetAvailable(last.BalanceAfter).Exec(ctx)
		}
	}
	res2, err := m.RunVerify(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Passed {
		for _, c := range res2.Checks {
			t.Logf("[%s] %s: %s", c.Status, c.Name, c.Message)
		}
		t.Fatal("修平差异后 verify 应通过")
	}
	// 破坏一单金额 → verify 应抓到
	o, _ := client.Order.Query().First(ctx)
	client.Order.UpdateOne(o).SetTotalAmount(o.TotalAmount + 1).Exec(ctx)
	res3, _ := m.RunVerify(ctx, 10)
	if res3.Passed {
		t.Fatal("金额被破坏后 verify 应失败")
	}
}
