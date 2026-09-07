package migratev1

// P3 卡密域全链路单测：sqlite 源（1.x 最小列集）+ ent sqlite 目标。
// 覆盖：密文/明文混合、CBC→GCM 重加密与 HMAC hash 重算、同商品重复卡跳过、
// locked 超时释放、used/disabled 状态、content_hash 漂移告警不阻断、
// order_deliveries 物理删卡回填、幂等重跑。

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newV1CardSource(t *testing.T) (*Source, []byte, []byte) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:migv1card%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", testDBSeq.Add(1)))
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
		`CREATE TABLE categories (id INTEGER PRIMARY KEY, merchant_id INTEGER, parent_id INTEGER, name TEXT, icon TEXT, sort INTEGER, status INTEGER, hide INTEGER)`,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, merchant_id INTEGER, category_id INTEGER, name TEXT, slug TEXT, description TEXT, cover TEXT, images TEXT,
			price INTEGER, factory_price INTEGER, draft_premium INTEGER, member_price TEXT, stock_type TEXT, fulfillment_type TEXT, delivery_message TEXT,
			stock_visible INTEGER, control_config TEXT, delivery_mode TEXT, dedup INTEGER, sort INTEGER, is_featured INTEGER, status INTEGER, hide INTEGER,
			upstream_source_id INTEGER, upstream_product_code TEXT, upstream_synced_at TEXT, virtual_reviews TEXT, deleted_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE card_imports (id INTEGER PRIMARY KEY, product_id INTEGER, operator_id INTEGER, source TEXT, total INTEGER, success_count INTEGER, failed_count INTEGER, skipped_count INTEGER, status TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE cards (id INTEGER PRIMARY KEY, product_id INTEGER, import_id INTEGER, order_id INTEGER, content TEXT, content_hash TEXT, status TEXT,
			locked_at TEXT, used_at TEXT, created_at TEXT, updated_at TEXT, note TEXT, card_type TEXT, owner_id INTEGER,
			draft_premium INTEGER, draft_cost INTEGER, price INTEGER, number_hash TEXT)`,
		`CREATE TABLE order_deliveries (id INTEGER PRIMARY KEY, order_id INTEGER, product_id INTEGER, card_content TEXT, delivered_mode TEXT, delivered_at TEXT)`,
		`CREATE TABLE roles (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE model_has_roles (role_id INTEGER, model_type TEXT, model_id INTEGER)`,
		`CREATE TABLE product_skus (id INTEGER PRIMARY KEY, product_id INTEGER, name TEXT, price INTEGER, upstream_sku_code TEXT, sort INTEGER, status INTEGER)`,
		`CREATE TABLE reviews (id INTEGER PRIMARY KEY, product_id INTEGER, user_id INTEGER, order_id INTEGER, rating INTEGER, content TEXT, status TEXT, created_at TEXT)`,
	})

	// 密钥：与 fixtures 同款派生（旧钥匙）
	fix := loadFixturesForTest(t)
	oldCardKey, _ := hexKey(fix.CardKey)
	_ = fix.AppKey

	p1 := sha256Hex("PLAIN-001")
	p2 := sha256Hex("CARD-密文-002")
	dup1 := sha256Hex("DUP-SAME-003")
	oldLocked := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02 15:04:05")
	freshLocked := time.Now().UTC().Format("2006-01-02 15:04:05")

	execScript(t, handle, []string{
		`INSERT INTO settings VALUES (1,'site_name','"卡密域测试"','storefront')`,
		`INSERT INTO user_groups VALUES (1,'普通会员','100.00',0,0,0,1)`,
		`INSERT INTO currencies VALUES (1,'CNY','人民币','¥','before',2,'1',1,1,0)`,
		`INSERT INTO products VALUES (1,1,NULL,'月卡','yueka','',NULL,NULL,1500,1000,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO card_imports VALUES (1,1,2,'batch.csv',100,98,1,1,'completed','2026-01-01 10:00:00','2026-01-01 10:00:00')`,
	})
	execScript(t, handle, []string{
		// 明文卡
		fmt.Sprintf(`INSERT INTO cards VALUES (1,1,1,NULL,'PLAIN-001','%s','unused',NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,'月卡',0,0,0,NULL,NULL)`, p1),
		// 密文卡（真实 Laravel 载荷）
		fmt.Sprintf(`INSERT INTO cards VALUES (2,1,1,NULL,'%s','%s','unused',NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00','备注','月卡',0,0,0,NULL,NULL)`,
			sealV1Card(t, oldCardKey, "CARD-密文-002"), p2),
		// 同商品同明文重复 ×2（1.x dedup_hash 可空导致）——其一应被跳过
		fmt.Sprintf(`INSERT INTO cards VALUES (3,1,NULL,NULL,'DUP-SAME-003','%s','unused',NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,NULL,NULL)`, dup1),
		fmt.Sprintf(`INSERT INTO cards VALUES (4,1,NULL,NULL,'DUP-SAME-003','%s','unused',NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,NULL,NULL)`, dup1),
		// locked 超时（→available）与新鲜锁（→reserved）
		fmt.Sprintf(`INSERT INTO cards VALUES (5,1,NULL,NULL,'LOCK-STALE-005','%s','locked','%s',NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,NULL,NULL)`,
			sha256Hex("LOCK-STALE-005"), oldLocked),
		fmt.Sprintf(`INSERT INTO cards VALUES (6,1,NULL,NULL,'LOCK-FRESH-006','%s','locked','%s',NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,NULL,NULL)`,
			sha256Hex("LOCK-FRESH-006"), freshLocked),
		// used / disabled + 靓号
		fmt.Sprintf(`INSERT INTO cards VALUES (7,1,NULL,900,'USED-007','%s','used',NULL,'2026-03-01 10:00:00','2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,'季卡',0,0,0,3000,'%s')`,
			sha256Hex("USED-007"), sha256Hex("88888")),
		fmt.Sprintf(`INSERT INTO cards VALUES (8,1,NULL,NULL,'DIS-008','%s','disabled',NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,NULL,NULL)`, sha256Hex("DIS-008")),
		// content_hash 漂移（明文改过但 hash 没更新）——应迁移成功 + 报告告警
		fmt.Sprintf(`INSERT INTO cards VALUES (9,1,NULL,NULL,'DRIFT-009','%s','unused',NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00',NULL,NULL,0,0,0,NULL,NULL)`, sha256Hex("not-matching")),
		// order_deliveries：#1 卡密仍存在（status 模式，跳过）/#2 已物理删（回填）/#3 分站商品
		`INSERT INTO order_deliveries VALUES (1,900,1,'USED-007','status','2026-03-01 10:00:00')`,
		`INSERT INTO order_deliveries VALUES (2,901,1,'DELETED-DELIVERY-Only','delete','2026-03-02 10:00:00')`,
		`INSERT INTO order_deliveries VALUES (3,902,99,'SUBSITE-DEL','delete','2026-03-03 10:00:00')`,
	})
	return &Source{DB: handle}, nil, oldCardKey
}

func TestMigrateP3Inventory(t *testing.T) {
	src, _, oldCardKey := newV1CardSource(t)
	client := newTestClient(t)
	rw, _ := NewReportWriter(t.TempDir())
	t.Cleanup(func() { _ = rw.Close() })

	newCardKey := deriveTestKey(t, "zcard2-card-key")
	m := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m.DataKey = deriveTestKey(t, "zcard2-data-key")
	m.NewCardKey = newCardKey
	m.CardKey = oldCardKey

	ctx := context.Background()
	// 依赖：先迁商品（P2 内核直接调 products 部分——本测试只需 products 就位）
	// 简化：直接跑完整 P0-P3（源里已含全部依赖表）
	for _, step := range []func(context.Context) error{m.MigrateSystem, m.MigrateIdentity, m.MigrateCatalog, m.MigrateInventory} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 卡密：9 行源 → 8 迁移（1 重复跳过）
	cards, _ := client.Card.Query().Order(ent.Asc(card.FieldID)).All(ctx)
	if len(cards) != 8+1 { // 8 主迁移 + 1 回填
		t.Fatalf("cards 期望 9 行（8 主 + 1 回填），实际 %d", len(cards))
	}
	cipher, _ := inventory.NewCardCipher(newCardKey)

	// 逐张解密断言
	byPlain := map[string]*ent.Card{}
	for _, c := range cards {
		plain, err := cipher.Open(c.Content, c.ProductID, 0)
		if err != nil {
			t.Fatalf("卡 %d 解密失败: %v", c.ID, err)
		}
		byPlain[plain] = c
		// hash 一致性：HMAC(plain) == content_hash
		if cipher.ContentHash(plain) != c.ContentHash {
			t.Fatalf("卡 %d content_hash 非 HMAC 重算值", c.ID)
		}
	}
	if c := byPlain["CARD-密文-002"]; c == nil || c.Note != "备注" || c.CardType != "月卡" || c.ImportID == 0 {
		t.Fatalf("密文卡字段异常: %+v", c)
	}
	if c := byPlain["PLAIN-001"]; c == nil || c.Status != "available" || c.ImportID == 0 {
		t.Fatalf("明文卡异常: %+v", c)
	}
	if c := byPlain["LOCK-STALE-005"]; c == nil || c.Status != "available" {
		t.Fatalf("超时锁应释放为 available: %+v", c)
	}
	if c := byPlain["LOCK-FRESH-006"]; c == nil || c.Status != "reserved" {
		t.Fatalf("新鲜锁应为 reserved: %+v", c)
	}
	if c := byPlain["USED-007"]; c == nil || c.Status != "used" || c.Price != 3000 || c.NumberHash != sha256Hex("88888") || c.OrderID != 0 {
		t.Fatalf("used 卡异常（order_id 应为 0 待 P4 回填）: %+v", c)
	}
	if c := byPlain["DIS-008"]; c == nil || c.Status != "disabled" {
		t.Fatalf("disabled 卡异常: %+v", c)
	}
	if c := byPlain["DRIFT-009"]; c == nil {
		t.Fatal("hash 漂移卡应正常迁移（仅告警）")
	}
	// 回填卡
	if c := byPlain["DELETED-DELIVERY-Only"]; c == nil || c.Status != "used" {
		t.Fatalf("物理删卡回填异常: %+v", c)
	} else if c.UsedAt.Format("2006-01-02") != "2026-03-02" {
		t.Fatalf("回填 used_at 异常: %v", c.UsedAt)
	}

	// 统计：9 源 - 1 重复 = 8 迁移；回填 1 / 跳过 2（存在 + 分站）
	st := m.Stats().Tables["cards"]
	if st.Migrated != 8 || st.SkippedExists != 1 || st.Failed != 0 {
		t.Fatalf("cards 统计异常: %+v", st)
	}
	bf := m.Stats().Tables["order_deliveries_backfill"]
	if bf.Migrated != 1 || bf.SkippedExists != 2 {
		t.Fatalf("回填统计异常: %+v", bf)
	}

	// 幂等重跑：全部跳过、无重复
	m2 := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "")
	m2.DataKey, m2.NewCardKey, m2.CardKey = m.DataKey, newCardKey, oldCardKey
	if err := m2.MigrateInventory(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m2.Stats().Tables["cards"]; got.Migrated != 0 || got.SkippedExists != 9 {
		t.Fatalf("重跑应全部幂等跳过（9 行含重复行）: %+v", got)
	}
	n, _ := client.Card.Query().Count(ctx)
	if n != 9 {
		t.Fatalf("重跑后卡密数异常: %d", n)
	}
}

// ---------- helpers ----------

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hexKey(s string) ([]byte, error) {
	return laracrypt.ParseKey(s)
}

// sealV1Card 构造 Laravel Encrypter 载荷（laracrypt 的正向逆操作，测试专用）。
func sealV1Card(t *testing.T, key []byte, plain string) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, aes.BlockSize)
	padLen := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append([]byte(plain), bytes.Repeat([]byte{byte(padLen)}, padLen)...)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(base64.StdEncoding.EncodeToString(iv)))
	mac.Write([]byte(base64.StdEncoding.EncodeToString(ct)))
	payload := map[string]string{
		"iv":    base64.StdEncoding.EncodeToString(iv),
		"value": base64.StdEncoding.EncodeToString(ct),
		"mac":   hex.EncodeToString(mac.Sum(nil)),
	}
	j, _ := json.Marshal(payload)
	return base64.StdEncoding.EncodeToString(j)
}
