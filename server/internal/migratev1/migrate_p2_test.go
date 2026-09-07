package migratev1

// P2 目录域全链路单测：sqlite 源 + ent sqlite 目标，跑 P0→P1→P2 三阶段。
// 覆盖：分类树/分站过滤、member_price key 重写、fixed 直发重加密、slug 超长截断、
// 软删→下架/隐藏、sku 停用与超长编码、virtual_reviews 展开、supply_mappings 生成。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newV1CatalogSource(t *testing.T) *Source {
	t.Helper()
	handle, err := db.SQLite.Open("file:migv1cat" + fmt.Sprint(testDBSeq.Add(1)) + "?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	execScript(t, handle, []string{
		// P0/P1 依赖表（最小列集）
		`CREATE TABLE currencies (id INTEGER PRIMARY KEY, code TEXT, name TEXT, symbol TEXT, symbol_position TEXT, decimal_places INTEGER, exchange_rate TEXT, is_base INTEGER, is_enabled INTEGER, sort INTEGER)`,
		`CREATE TABLE user_groups (id INTEGER PRIMARY KEY, name TEXT, discount TEXT, min_recharge INTEGER, min_consumption INTEGER, sort INTEGER, status INTEGER)`,
		`CREATE TABLE settings (id INTEGER PRIMARY KEY, key TEXT, value TEXT, "group" TEXT)`,
		`CREATE TABLE supply_sources (id INTEGER PRIMARY KEY, name TEXT, driver TEXT, base_url TEXT, credentials TEXT, status TEXT, settings TEXT, balance_cache INTEGER, last_synced_at TEXT, last_error TEXT, deleted_at TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, name TEXT, email TEXT, phone TEXT, password TEXT, status INTEGER, deleted_at TEXT, balance INTEGER, points INTEGER, pid INTEGER, group_id INTEGER, last_login_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE roles (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE model_has_roles (role_id INTEGER, model_type TEXT, model_id INTEGER)`,
		// P2 表
		`CREATE TABLE categories (id INTEGER PRIMARY KEY, merchant_id INTEGER, parent_id INTEGER, name TEXT, icon TEXT, sort INTEGER, status INTEGER, hide INTEGER)`,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, merchant_id INTEGER, category_id INTEGER, name TEXT, slug TEXT, description TEXT, cover TEXT, images TEXT,
			price INTEGER, factory_price INTEGER, draft_premium INTEGER, member_price TEXT, stock_type TEXT, fulfillment_type TEXT, delivery_message TEXT,
			stock_visible INTEGER, control_config TEXT, delivery_mode TEXT, dedup INTEGER, sort INTEGER, is_featured INTEGER, status INTEGER, hide INTEGER,
			upstream_source_id INTEGER, upstream_product_code TEXT, upstream_synced_at TEXT, virtual_reviews TEXT, deleted_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE product_skus (id INTEGER PRIMARY KEY, product_id INTEGER, name TEXT, price INTEGER, upstream_sku_code TEXT, sort INTEGER, status INTEGER)`,
		`CREATE TABLE reviews (id INTEGER PRIMARY KEY, product_id INTEGER, user_id INTEGER, order_id INTEGER, rating INTEGER, content TEXT, status TEXT, created_at TEXT)`,
	})
	longSlug := strings.Repeat("s", 250)
	execScript(t, handle, []string{
		`INSERT INTO currencies VALUES (1,'CNY','人民币','¥','before',2,'1',1,1,0)`,
		`INSERT INTO user_groups VALUES (1,'普通会员','100.00',0,0,0,1)`,
		`INSERT INTO user_groups VALUES (2,'黄金会员','80.00',100000,500000,1,1)`,
		`INSERT INTO settings VALUES (1,'site_name','"目录域测试店"','storefront')`,
		`INSERT INTO supply_sources VALUES (1,'测试上游','zcard','https://up.example.com','{"token":"t1"}','active',NULL,0,NULL,NULL,NULL)`,
		`INSERT INTO users VALUES (1,'bob','','b@x.com','','x',1,NULL,0,0,0,2,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		// 分类：c1 根 / c2 子 / c3 分站（跳过）
		`INSERT INTO categories VALUES (1,1,0,'账号类','',0,1,0)`,
		`INSERT INTO categories VALUES (2,1,1,'月卡类','',1,1,0)`,
		`INSERT INTO categories VALUES (3,2,0,'分站分类','',0,1,0)`,
		// 商品：p1 会员价 / p2 fixed 直发 / p3 隐藏 / p4 软删 / p5 分站 / p6 上游+虚拟评论 / p7 超长 slug
		`INSERT INTO products VALUES (1,1,1,'月卡A','yueka-a','详情','c.jpg','["a.jpg"]',1500,1000,0,'{"2":1200}','card','auto_card',NULL,1,NULL,'status',1,0,1,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO products VALUES (2,1,1,'网盘直发','wangpan','详情','',NULL,800,500,0,NULL,'url','fixed','链接 https://pan.example/x 提取码 ab12',1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-02 10:00:00','2026-01-02 10:00:00')`,
		`INSERT INTO products VALUES (3,1,2,'隐藏品','hidden-p','',NULL,NULL,900,0,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,1,NULL,NULL,NULL,NULL,NULL,'2026-01-02 10:00:00','2026-01-02 10:00:00')`,
		`INSERT INTO products VALUES (4,1,2,'已删品','deleted-p','',NULL,NULL,900,0,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,'2026-06-01 00:00:00','2026-01-03 10:00:00','2026-01-03 10:00:00')`,
		`INSERT INTO products VALUES (5,2,NULL,'分站品','subsite-p','',NULL,NULL,700,0,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-04 10:00:00','2026-01-04 10:00:00')`,
		`INSERT INTO products VALUES (6,1,1,'上游代发','upstream-p','',NULL,NULL,1200,900,0,NULL,'card','upstream',NULL,1,NULL,'status',1,0,0,1,0,1,'UP-100','2026-05-01 00:00:00','{"rating":4.8,"count":156,"list":[{"nickname":"小明","content":"很好用","rating":5},{"nickname":"小红","content":"发货快"}]}',NULL,'2026-01-05 10:00:00','2026-01-05 10:00:00')`,
		`INSERT INTO products VALUES (7,1,1,'超长slug品','` + longSlug + `','',NULL,NULL,600,0,0,NULL,'card','auto_card',NULL,1,NULL,'status',1,0,0,1,0,NULL,NULL,NULL,NULL,NULL,'2026-01-06 10:00:00','2026-01-06 10:00:00')`,
		// SKU：s1 正常 / s2 停用 / s3 分站商品 / s4 超长上游编码
		`INSERT INTO product_skus VALUES (1,1,'月卡·季卡',1200,NULL,0,1)`,
		`INSERT INTO product_skus VALUES (2,1,'停用规格',999,NULL,1,0)`,
		`INSERT INTO product_skus VALUES (3,5,'分站规格',100,NULL,0,1)`,
		`INSERT INTO product_skus VALUES (4,1,'超长编码规格',100,'` + strings.Repeat("K", 70) + `',0,1)`,
		// 评论：r1 正常 / r2 分站商品
		`INSERT INTO reviews VALUES (1,1,1,101,5,'非常好用','approved','2026-02-01 10:00:00')`,
		`INSERT INTO reviews VALUES (2,5,1,102,4,'分站评论','approved','2026-02-02 10:00:00')`,
	})
	return &Source{DB: handle}
}

func TestMigrateP2Catalog(t *testing.T) {
	src := newV1CatalogSource(t)
	client := newTestClient(t)
	rw, err := NewReportWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rw.Close() })

	newCardKey := deriveTestKey(t, "zcard2-new-card-key")
	m := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "Asia/Shanghai")
	m.DataKey = deriveTestKey(t, "zcard2-data-key")
	m.NewCardKey = newCardKey

	ctx := context.Background()
	for _, step := range []func(context.Context) error{m.MigrateSystem, m.MigrateIdentity, m.MigrateCatalog} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 分类：2 行（分站跳过），树结构保持
	cats, _ := client.Category.Query().All(ctx)
	if len(cats) != 2 {
		t.Fatalf("categories 期望 2 行，实际 %d", len(cats))
	}
	var root, child *ent.Category
	for _, c := range cats {
		if c.Name == "账号类" {
			root = c
		}
		if c.Name == "月卡类" {
			child = c
		}
	}
	if root == nil || child == nil || child.ParentID != root.ID {
		t.Fatalf("分类树异常: root=%v child=%v", root, child)
	}

	// 商品：6 行（p5 分站跳过）
	prods, _ := client.Product.Query().All(ctx)
	if len(prods) != 6 {
		t.Fatalf("products 期望 6 行，实际 %d", len(prods))
	}
	bySlug := map[string]*ent.Product{}
	for _, p := range prods {
		bySlug[p.Slug] = p
	}
	p1 := bySlug["yueka-a"]
	if p1 == nil || p1.Price != 1500 || p1.FactoryPrice != 1000 || p1.IsRecommend != true {
		t.Fatalf("p1 基础字段异常: %+v", p1)
	}
	// member_price：key 由 1.x 组 2 → member_level 新 ID（黄金=第 2 个迁入 → ID 2）
	if v, ok := p1.MemberPrice["2"]; !ok || v != 1200 {
		t.Fatalf("member_price 重写异常: %v", p1.MemberPrice)
	}
	// fixed 直发：CardCipher 可解回明文（AAD=product/subsite=0）
	p2 := bySlug["wangpan"]
	if p2 == nil || len(p2.DirectContent) == 0 {
		t.Fatalf("direct_content 未迁移: %+v", p2)
	}
	cipher, _ := inventory.NewCardCipher(newCardKey)
	plain, err := cipher.Open(p2.DirectContent, p2.ID, 0)
	if err != nil || plain != "链接 https://pan.example/x 提取码 ab12" {
		t.Fatalf("direct_content 解密异常: %v %q", err, plain)
	}
	// 状态映射：隐藏→2、软删→0
	if p3 := bySlug["hidden-p"]; p3 == nil || p3.Status != 2 {
		t.Fatalf("hide 应映射 status=2: %+v", p3)
	}
	if p4 := bySlug["deleted-p"]; p4 == nil || p4.Status != 0 {
		t.Fatalf("软删应映射 status=0: %+v", p4)
	}
	// 超长 slug：≤150 且含 -v1-<oldID>
	p7 := bySlug[strings.Repeat("s", 140)+"-v1-7"]
	if p7 == nil {
		t.Fatalf("超长 slug 未按预期截断：%v", keysOf(bySlug))
	}
	// 上游商品：supply_mappings 生成 + 虚拟评论展开
	p6 := bySlug["upstream-p"]
	if p6 == nil || p6.UpstreamSourceID == 0 || p6.UpstreamProductCode != "UP-100" {
		t.Fatalf("上游字段异常: %+v", p6)
	}
	maps, _ := client.SupplyMapping.Query().Where(supplymapping.UpstreamProduct("UP-100")).All(ctx)
	if len(maps) != 1 || maps[0].LocalProductID != p6.ID {
		t.Fatalf("supply_mappings 生成异常: %v", maps)
	}
	vrs, _ := client.VirtualReview.Query().All(ctx)
	if len(vrs) != 2 || vrs[0].Nickname != "小明" || vrs[1].Rating != 5 {
		t.Fatalf("virtual_reviews 展开异常: %v", vrs)
	}

	// SKU：1 迁移 + 2 跳过（停用/分站）+ 1 失败（超长编码）
	skus, _ := client.ProductSku.Query().All(ctx)
	if len(skus) != 1 || skus[0].Name != "月卡·季卡" || skus[0].Price != 1200 {
		t.Fatalf("product_skus 异常: %v", skus)
	}
	st := m.Stats().Tables["product_skus"]
	if st.Migrated != 1 || st.SkippedExists != 2 || st.Failed != 1 {
		t.Fatalf("product_skus 统计异常: %+v", st)
	}

	// 评论：1 行（分站跳过）
	revs, _ := client.Review.Query().All(ctx)
	if len(revs) != 1 || revs[0].Content != "非常好用" || revs[0].Status != "approved" {
		t.Fatalf("reviews 异常: %v", revs)
	}

	// 幂等重跑：全部跳过、无重复
	m2 := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "continue"}, "Asia/Shanghai")
	m2.DataKey, m2.NewCardKey = m.DataKey, newCardKey
	if err := m2.MigrateCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m2.Stats().Tables["products"]; got.Migrated != 0 || got.SkippedExists != 7 {
		t.Fatalf("重跑应全部幂等/分站跳过: %+v", got)
	}
	n, _ := client.Product.Query().Count(ctx)
	if n != 6 {
		t.Fatalf("重跑后商品数异常: %d", n)
	}
	if n, _ := client.VirtualReview.Query().Count(ctx); n != 2 {
		t.Fatalf("重跑后虚拟评论重复展开: %d", n)
	}

	// supply_mappings 幂等
	if err := m2.generateSupplyMappings(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := client.SupplyMapping.Query().Count(ctx); n != 1 {
		t.Fatalf("supply_mappings 重跑重复: %d", n)
	}
}

func keysOf(m map[string]*ent.Product) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
