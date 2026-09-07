package migratev1

// P6 尾域单测：主站先行（P0-P5，分站跳过）→ P6（分站 profile 建立 → 分站数据自动补迁）
// + 供货域（api_secret 解密重加密）+ 日志聚合。核心断言：分站补迁后商品/订单的
// subsite_id 与 profile 一致、卡密 AAD 用分站 subsite。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newV1FullSource(t *testing.T) *Source {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:migv1full%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", testDBSeq.Add(1)))
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
		`CREATE TABLE bills (id INTEGER PRIMARY KEY, user_id INTEGER, amount INTEGER, balance_after INTEGER, type INTEGER, log TEXT, order_id INTEGER, admin_id INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE withdrawals (id INTEGER PRIMARY KEY, user_id INTEGER, amount INTEGER, actual_amount INTEGER, fee INTEGER, method TEXT,
			account TEXT, account_name TEXT, status TEXT, reject_reason TEXT, admin_id INTEGER, processed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE commissions (id INTEGER PRIMARY KEY, order_id INTEGER, buyer_id INTEGER, referrer_id INTEGER, tier INTEGER, rate REAL,
			base_amount INTEGER, amount INTEGER, status TEXT, created_at TEXT, updated_at TEXT)`,
		// 分站域
		`CREATE TABLE merchants (id INTEGER PRIMARY KEY, user_id INTEGER, name TEXT, slug TEXT, status INTEGER, commission_rate REAL, settings TEXT, created_at TEXT, updated_at TEXT, deleted_at TEXT)`,
		`CREATE TABLE merchant_members (id INTEGER PRIMARY KEY, merchant_id INTEGER, user_id INTEGER, role TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE subsite_domains (id INTEGER PRIMARY KEY, merchant_id INTEGER, domain TEXT, type TEXT, verification_token TEXT,
			verification_status TEXT, status TEXT, is_primary INTEGER, verified_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE subsite_product_settings (id INTEGER PRIMARY KEY, merchant_id INTEGER, product_id INTEGER, sku_id INTEGER, is_listed INTEGER,
			pricing_mode TEXT, markup_percent REAL, fixed_markup_amount INTEGER, fixed_price_amount INTEGER, sort_order INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE subsite_ledger_entries (id INTEGER PRIMARY KEY, merchant_id INTEGER, order_id INTEGER, type TEXT, amount INTEGER, status TEXT,
			available_at TEXT, withdraw_request_id INTEGER, idempotency_key TEXT, remark TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE subsite_order_snapshots (id INTEGER PRIMARY KEY, order_id INTEGER, merchant_id INTEGER, domain TEXT, reseller_user_id INTEGER, buyer_id INTEGER,
			base_amount INTEGER, reseller_amount INTEGER, profit_amount INTEGER, created_at TEXT, updated_at TEXT)`,
		// 供货域
		`CREATE TABLE supplier_accounts (id INTEGER PRIMARY KEY, user_id INTEGER, name TEXT, api_key TEXT, api_secret TEXT, balance INTEGER,
			status TEXT, approved INTEGER, contact TEXT, remark TEXT, created_at TEXT, updated_at TEXT, deleted_at TEXT)`,
		`CREATE TABLE supplier_product_prices (id INTEGER PRIMARY KEY, supplier_account_id INTEGER, product_id INTEGER, sku_id INTEGER, price INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE supplier_ledger_entries (id INTEGER PRIMARY KEY, supplier_account_id INTEGER, order_id INTEGER, type TEXT, amount INTEGER, balance_after INTEGER,
			idempotency_key TEXT, remark TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE supply_orders (id INTEGER PRIMARY KEY, supplier_account_id INTEGER, order_id INTEGER, downstream_order_no TEXT,
			fulfillment_mode TEXT, callback_url TEXT, callback_status TEXT, created_at TEXT, updated_at TEXT)`,
		// 内容/日志
		`CREATE TABLE media (id INTEGER PRIMARY KEY, category_id INTEGER, original_name TEXT, filename TEXT, path TEXT, url TEXT,
			mime_type TEXT, size INTEGER, width INTEGER, height INTEGER, created_at TEXT, updated_at TEXT, deleted_at TEXT)`,
		`CREATE TABLE visit_logs (id INTEGER PRIMARY KEY, ip TEXT, user_agent TEXT, path TEXT, created_at TEXT)`,
		`CREATE TABLE security_audit_logs (id INTEGER PRIMARY KEY, actor_id INTEGER, source TEXT, action TEXT, target_type TEXT, target_id TEXT,
			method TEXT, path TEXT, status_code INTEGER, ip TEXT, user_agent TEXT, metadata TEXT, created_at TEXT)`,
	})
	execScript(t, handle, []string{
		`INSERT INTO settings VALUES (1,'site_name','"全量测试"','storefront')`,
		`INSERT INTO user_groups VALUES (1,'普通会员','100.00',0,0,0,1)`,
		`INSERT INTO currencies VALUES (1,'CNY','人民币','¥','before',2,'1',1,1,0)`,
		`INSERT INTO users VALUES (1,'owner','','o@x.com','','x',1,NULL,0,0,0,1,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		// 主站 + 分站商品
		`INSERT INTO products VALUES (1,1,NULL,'主站卡','main-card','',NULL,NULL,1500,800,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO products VALUES (2,2,NULL,'分站卡','sub-card','',NULL,NULL,1800,1500,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		// 分站商户 + owner + 域名 + 定价 + 账本
		`INSERT INTO merchants VALUES (2,1,'分站A','site-a',1,10.0,NULL,'2026-01-02 10:00:00','2026-01-02 10:00:00',NULL)`,
		`INSERT INTO merchant_members VALUES (1,2,1,'owner','2026-01-02 10:00:00','2026-01-02 10:00:00')`,
		`INSERT INTO subsite_domains VALUES (1,2,'a.example.com','custom','tok1','verified','active',1,NULL,'2026-01-03 10:00:00','2026-01-03 10:00:00')`,
		`INSERT INTO subsite_product_settings VALUES (1,2,2,0,1,'markup_percent',20.0,0,0,0,'2026-01-04 10:00:00','2026-01-04 10:00:00')`,
		`INSERT INTO subsite_ledger_entries VALUES (1,2,NULL,'manual_adjust',30000,'available',NULL,NULL,'v1-sle-1','初始','2026-01-05 10:00:00','2026-01-05 10:00:00')`,
		// 分站订单（含利润拆分）
		`INSERT INTO orders VALUES (2,'SUB-ORD-1',2,1,2,1,1800,0,1500,NULL,'CNY',NULL,NULL,NULL,'paid','delivered','auto_card',NULL,2,'a.example.com',300,NULL,NULL,NULL,NULL,NULL,NULL,'o@x.com',NULL,NULL,'2026-02-01 10:00:00',NULL,'2026-02-01 09:00:00','2026-02-01 10:00:00')`,
		// 分站卡密
		`INSERT INTO cards VALUES (2,2,NULL,2,'SUB-CARD-001','` + sha256Hex("SUB-CARD-001") + `','used',NULL,'2026-02-01 10:05:00','2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,0,NULL)`,
		`INSERT INTO order_deliveries VALUES (1,2,2,'SUB-CARD-001','status','2026-02-01 10:05:00')`,
		// 供货域（api_secret 用 fixtures 密文）
		`INSERT INTO supplier_accounts VALUES (1,NULL,'下游甲','sk12345','` + fixturePayloadForTest(t, "crypt_string_ascii") + `',50000,'active',1,'wx: jia','备注','2026-01-06 10:00:00','2026-01-06 10:00:00',NULL)`,
		`INSERT INTO supplier_product_prices VALUES (1,1,1,0,1200,'2026-01-06 10:00:00','2026-01-06 10:00:00')`,
		`INSERT INTO supplier_ledger_entries VALUES (1,1,NULL,'recharge',50000,50000,'v1-sup-1','预存','2026-01-06 10:00:00','2026-01-06 10:00:00')`,
		`INSERT INTO supply_orders VALUES (1,1,1,'DOWN-001','sync','https://down.example/cb','sent','2026-01-07 10:00:00','2026-01-07 10:00:00')`,
		// 媒体 + 日志
		`INSERT INTO media VALUES (1,NULL,'logo.png','logo.png','logo.png','https://x/storage/logo.png','image/png',1024,64,64,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL)`,
		`INSERT INTO visit_logs VALUES (1,'1.1.1.1','ua','/','2020-01-01 10:00:00')`,
		`INSERT INTO visit_logs VALUES (2,'1.1.1.2','ua','/','` + nowUTCStr() + `')`,
		`INSERT INTO visit_logs VALUES (3,'1.1.1.3','ua','/','` + nowUTCStr() + `')`,
		`INSERT INTO security_audit_logs VALUES (1,1,'admin','admin.login',NULL,NULL,'POST','/api/auth/login',200,'2.2.2.2','ua',NULL,'` + nowUTCStr() + `')`,
	})
	return &Source{DB: handle}
}

func nowUTCStr() string {
	return "2026-09-07 10:00:00"
}

func fixturePayloadForTest(t *testing.T, name string) string {
	t.Helper()
	fix := loadFixturesForTest(t)
	return fixturePayload(t, fix, name)
}

func TestMigrateP6Full(t *testing.T) {
	// media 存储根重定向到临时目录（防相对路径产物污染仓库）
	oldRoot := media.StorageRoot
	media.StorageRoot = filepath.Join(t.TempDir(), "uploads")
	t.Cleanup(func() { media.StorageRoot = oldRoot })
	src := newV1FullSource(t)
	client := newTestClient(t)
	rw, _ := NewReportWriter(t.TempDir())
	t.Cleanup(func() { _ = rw.Close() })

	fix := loadFixturesForTest(t)
	appKey, _ := laracrypt.ParseKey(fix.AppKey)
	newCardKey := deriveTestKey(t, "ck-new")
	dataKey := deriveTestKey(t, "dk-new")

	// 媒体文件源目录
	oldStorage := t.TempDir()
	os.MkdirAll(filepath.Join(oldStorage, "storage", "app", "public"), 0o755)
	os.WriteFile(filepath.Join(oldStorage, "storage", "app", "public", "logo.png"), []byte("fake-png-bytes"), 0o644)

	ctx := context.Background()

	// 阶段一：主站先行（P0-P5）——分站数据全部跳过
	m1 := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m1.AppKey, m1.DataKey, m1.NewCardKey = appKey, dataKey, newCardKey
	for _, step := range []func(context.Context) error{
		m1.MigrateSystem, m1.MigrateIdentity, m1.MigrateCatalog, m1.MigrateInventory, m1.MigrateTrade, m1.MigrateMoney,
	} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := m1.Stats().Tables["products"]; got.Migrated != 1 || got.SkippedExists != 1 {
		t.Fatalf("主站先行应只迁 1 商品（分站跳过）: %+v", got)
	}
	if got := m1.Stats().Tables["orders"]; got.Migrated != 0 || got.SkippedExists != 1 {
		t.Fatalf("主站先行订单（源仅分站单，应全跳过）: %+v", got)
	}

	// 阶段二：P6 分站补迁 + 供货 + 内容
	m2 := NewMigrator(src, client, NewIDMapper(client), rw, Options{
		Batch: 100, OnError: "continue", VisitDays: 90, AuditMonths: 12, OldStorageRoot: oldStorage,
	}, "")
	m2.AppKey, m2.DataKey, m2.NewCardKey = appKey, dataKey, newCardKey
	for _, step := range []func(context.Context) error{
		m2.MigrateReseller, m2.MigrateSuppliers, m2.MigrateContent,
	} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 分站 profile：1 个，owner 关联
	profiles, _ := client.ResellerProfile.Query().All(ctx)
	if len(profiles) != 1 || profiles[0].Status != "approved" {
		t.Fatalf("reseller_profiles 异常: %v", profiles)
	}
	siteID := profiles[0].ID

	// 分站数据补迁：商品/订单 subsite_id == profile.ID
	subProducts, _ := client.Product.Query().Where(product.Slug("sub-card")).All(ctx)
	if len(subProducts) != 1 || subProducts[0].SubsiteID != siteID {
		t.Fatalf("分站商品 subsite 异常: %+v（site=%d）", subProducts, siteID)
	}
	subOrders, _ := client.Order.Query().Where(order.OrderNo("SUB-ORD-1")).All(ctx)
	if len(subOrders) != 1 || subOrders[0].SubsiteID != siteID {
		t.Fatalf("分站订单 subsite 异常: %+v", subOrders)
	}
	// 分站订单金额行：base(1500)+markup(300)=1800 == total
	lines, _ := client.OrderAmountLine.Query().All(ctx)
	var subSum int64
	for _, l := range lines {
		if l.OrderID == subOrders[0].ID {
			subSum += l.Amount
		}
	}
	if subSum != 1800 {
		t.Fatalf("分站订单金额恒等式破坏: %d", subSum)
	}
	// 分站卡密：AAD 用分站 subsite 解密成功
	subCards, _ := client.Card.Query().All(ctx)
	var subCard *ent.Card
	for _, c := range subCards {
		if c.ProductID == subProducts[0].ID {
			subCard = c
		}
	}
	if subCard == nil {
		t.Fatal("分站卡未补迁")
	}
	cipher, _ := inventory.NewCardCipher(newCardKey)
	if plain, err := cipher.Open(subCard.Content, subCard.ProductID, siteID); err != nil || plain != "SUB-CARD-001" {
		t.Fatalf("分站卡密 AAD 解密失败（应绑定分站 subsite）: %v %q", err, plain)
	}
	// 分站卡 order_id 回填
	if subCard.OrderID != subOrders[0].ID {
		t.Fatalf("分站卡 order_id 未回填: %+v", subCard)
	}

	// 站点 + 定价 + 账本
	sites, _ := client.ResellerSite.Query().All(ctx)
	if len(sites) != 1 || sites[0].Domain != "a.example.com" || sites[0].VerificationStatus != "verified" {
		t.Fatalf("reseller_sites 异常: %v", sites)
	}
	prs, _ := client.ResellerPricing.Query().All(ctx)
	if len(prs) != 1 || prs[0].Mode != "markup_percent" || prs[0].Value != 2000 {
		t.Fatalf("reseller_pricings 异常（20%% → 2000 万分比）: %v", prs)
	}
	lgs, _ := client.ResellerLedgerEntry.Query().All(ctx)
	if len(lgs) != 1 || lgs[0].Amount != 30000 || string(lgs[0].Status) != "available" {
		t.Fatalf("reseller_ledger 异常: %v", lgs)
	}
	bal, _ := client.ResellerBalanceAccount.Query().All(ctx)
	if len(bal) != 1 || bal[0].SubsiteID != siteID || bal[0].Available != 30000 {
		t.Fatalf("分站余额快照异常: %v", bal)
	}

	// 供货域：api_secret 解密重加密可解回
	sup, _ := client.SupplierAccount.Query().All(ctx)
	if len(sup) != 1 || sup[0].BalanceCache != 50000 || string(sup[0].Status) != "approved" {
		t.Fatalf("supplier_accounts 异常: %v", sup)
	}
	box, _ := crypto.NewBox(dataKey)
	secret, err := box.Open(sup[0].APISecret, nil)
	if err != nil || string(secret) != "hello zcard" {
		t.Fatalf("api_secret 重加密解回失败: %v %q（期望 fixtures 明文 hello zcard）", err, secret)
	}
	supPrices, _ := client.SupplierProductPrice.Query().All(ctx)
	if len(supPrices) != 1 || supPrices[0].Price != 1200 {
		t.Fatalf("supplier_product_prices 异常: %v", supPrices)
	}
	supLedger, _ := client.SupplierLedgerEntry.Query().All(ctx)
	if len(supLedger) != 1 || supLedger[0].Reference != "v1-sup-1" {
		t.Fatalf("supplier_ledger 异常: %v", supLedger)
	}
	supOrders, _ := client.SupplyOrder.Query().All(ctx)
	if len(supOrders) != 1 || supOrders[0].DownstreamOrderNo != "DOWN-001" {
		t.Fatalf("supply_orders 异常: %v", supOrders)
	}
	cbs, _ := client.DownstreamCallback.Query().All(ctx)
	if len(cbs) != 1 || cbs[0].CallbackURL != "https://down.example/cb" {
		t.Fatalf("downstream_callbacks 异常: %v", cbs)
	}

	// 媒体：行 + 文件 + sha256
	medias, _ := client.Media.Query().All(ctx)
	if len(medias) != 1 || medias[0].Sha256 == "" {
		t.Fatalf("media 异常: %v", medias)
	}
	if _, err := os.Stat(filepath.Join(media.StorageRoot, "logo.png")); err != nil {
		t.Fatalf("媒体文件未复制: %v", err)
	}

	// 访问日志：仅窗口内聚合（2020-01-01 排除；2 条 → 1 聚合行）
	vls, _ := client.VisitLog.Query().All(ctx)
	if len(vls) != 1 || vls[0].Pv != 2 || vls[0].Uv != 2 {
		t.Fatalf("visit_logs 聚合异常: %v", vls)
	}
	// 审计：1 行
	als, _ := client.SecurityAuditLog.Query().All(ctx)
	if len(als) != 1 || string(als[0].ActorType) != "admin" || als[0].Metadata["v1_path"] != "/api/auth/login" {
		t.Fatalf("audit 异常: %v", als)
	}
}
