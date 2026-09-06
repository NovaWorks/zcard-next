package migratev1

// P0+P1 全链路单测：sqlite 内存库模拟 1.x 源（双方言扫描 SQL），ent sqlite 目标。
// 覆盖：状态/枚举映射、金额直传、settings 首批映射与 v1_legacy、SECRET 跳过、
// bcrypt 直迁、钱包/积分快照、邀请链（含环防护）、admin 提取、凭据重加密、幂等重跑。

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pointaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/walletaccount"
	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// newV1Source 建 1.x 形状最小源库并播种。
func newV1Source(t *testing.T) (*Source, map[string]string) {
	t.Helper()
	handle, err := db.SQLite.Open("file:migv1src?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	execScript(t, handle, []string{
		`CREATE TABLE currencies (id INTEGER PRIMARY KEY, code TEXT, name TEXT, symbol TEXT, symbol_position TEXT, decimal_places INTEGER, exchange_rate TEXT, is_base INTEGER, is_enabled INTEGER, sort INTEGER)`,
		`CREATE TABLE user_groups (id INTEGER PRIMARY KEY, name TEXT, discount TEXT, min_recharge INTEGER, min_consumption INTEGER, sort INTEGER, status INTEGER)`,
		`CREATE TABLE settings (id INTEGER PRIMARY KEY, key TEXT, value TEXT, "group" TEXT)`,
		`CREATE TABLE supply_sources (id INTEGER PRIMARY KEY, name TEXT, driver TEXT, base_url TEXT, credentials TEXT, status TEXT, settings TEXT, balance_cache INTEGER, last_synced_at TEXT, last_error TEXT, deleted_at TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, name TEXT, email TEXT, phone TEXT, password TEXT, status INTEGER, deleted_at TEXT, balance INTEGER, points INTEGER, pid INTEGER, group_id INTEGER, last_login_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE roles (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE model_has_roles (role_id INTEGER, model_type TEXT, model_id INTEGER)`,
	})
	// 密钥：与 laracrypt fixtures 同款确定性派生
	appKeyRaw := deriveTestKey(t, "zcard-v1-fixtures-app-key")
	fix := loadFixturesForTest(t)
	execScript(t, handle, []string{
		`INSERT INTO currencies VALUES (1,'CNY','人民币','¥','before',2,'1',1,1,0)`,
		`INSERT INTO currencies VALUES (2,'USD','美元','$','after',2,'0.14000000',0,0,1)`,
		`INSERT INTO user_groups VALUES (1,'普通会员','100.00',0,0,0,1)`,
		`INSERT INTO user_groups VALUES (2,'黄金会员','80.00',100000,500000,1,1)`,
		`INSERT INTO settings VALUES (1,'site_name','"我的发卡店"','storefront')`,
		`INSERT INTO settings VALUES (2,'maintenance_mode','"false"','storefront')`, // 字符串化 bool 历史形态
		`INSERT INTO settings VALUES (3,'order_close_minutes','30','storefront')`,
		`INSERT INTO settings VALUES (4,'require_contact','"email"','storefront')`,
		`INSERT INTO settings VALUES (5,'mail_password','"whatever"','storefront')`, // SECRET：应跳过
		`INSERT INTO settings VALUES (6,'some_unknown_key','{"a":1}','storefront')`,
		// bob 为超管；dave/eve 组成分销链；frank/grace 构成 pid 环
		`INSERT INTO users VALUES (1,'alice','','a@x.com','','',0,NULL,0,0,0,1,NULL,'2026-01-01 10:00:00','2026-01-02 10:00:00')`,
		`INSERT INTO users VALUES (2,'bob','店长bob','b@x.com','13800000002','` + bcryptY(t, "bob-pass-123") + `',1,NULL,12000,5,0,2,'2026-02-01 08:00:00','2026-01-01 10:00:00','2026-01-01 10:00:00')`,
		`INSERT INTO users VALUES (3,'carol','','','','',1,'2026-03-01 00:00:00',0,0,0,1,NULL,'2026-01-05 10:00:00','2026-01-05 10:00:00')`,
		`INSERT INTO users VALUES (4,'dave','','d@x.com','','',1,NULL,0,0,2,1,NULL,'2026-01-10 10:00:00','2026-01-10 10:00:00')`,
		`INSERT INTO users VALUES (5,'eve','','','','',1,NULL,0,0,4,1,NULL,'2026-01-11 10:00:00','2026-01-11 10:00:00')`,
		`INSERT INTO users VALUES (6,'frank','','','','',1,NULL,0,0,7,1,NULL,'2026-01-12 10:00:00','2026-01-12 10:00:00')`,
		`INSERT INTO users VALUES (7,'grace','','','','',1,NULL,0,0,6,1,NULL,'2026-01-12 10:00:00','2026-01-12 10:00:00')`,
		`INSERT INTO roles VALUES (1,'super_admin')`,
		`INSERT INTO model_has_roles VALUES (1,'` + morphType() + `',2)`,
	})
	// supply credentials：用真实 Laravel 密文（fixtures 的 crypt_string_json 向量，期望明文为 JSON）
	cred := fixturePayload(t, fix, "crypt_string_json")
	execScript(t, handle, []string{
		`INSERT INTO supply_sources VALUES (1,'上游A','dujiao_next','https://up.example.com','` + cred + `','active','{"stock_mode":"real"}',5000,'2026-05-01 00:00:00',NULL,NULL)`,
	})
	return &Source{DB: handle}, map[string]string{"appKey": string(appKeyRaw)}
}

func execScript(t *testing.T, handle *sql.DB, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := handle.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s[:min(60, len(s))], err)
		}
	}
}

func morphType() string { return `App\Models\User` }

// bcryptY 生成 $2y$ 前缀哈希（模拟 Laravel password_hash 产物；$2y$↔$2a$ 算法等价）。
func bcryptY(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	y := "$2y$" + string(h[4:])
	if err := bcrypt.CompareHashAndPassword([]byte(y), []byte(plain)); err != nil {
		t.Fatalf("$2y$ 哈希 Go 校验失败（架构假设不成立）: %v", err)
	}
	return y
}

func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func deriveTestKey(t *testing.T, seed string) []byte {
	t.Helper()
	sum := sha256Sum(seed)
	return sum[:32]
}

func runMigrate(t *testing.T, src *Source, client *ent.Client, appKey, dataKey []byte) *Migrator {
	t.Helper()
	rw, err := NewReportWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rw.Close() })
	m := NewMigrator(src, client, NewIDMapper(client), rw, Options{Batch: 100, OnError: "abort"}, "Asia/Shanghai")
	m.AppKey = appKey
	m.DataKey = dataKey
	if err := m.MigrateSystem(context.Background()); err != nil {
		t.Fatalf("P0 失败: %v", err)
	}
	if err := m.MigrateIdentity(context.Background()); err != nil {
		t.Fatalf("P1 失败: %v", err)
	}
	return m
}

func TestMigrateP0P1(t *testing.T) {
	src, keys := newV1Source(t)
	client := newTestClient(t)
	fix := loadFixturesForTest(t)
	_ = keys
	appKey, err := laracrypt.ParseKey(fix.AppKey)
	if err != nil {
		t.Fatal(err)
	}
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(i)
	}
	m := runMigrate(t, src, client, appKey, dataKey)
	ctx := context.Background()

	// currencies：2 行，USD position=suffix、rate 换算
	usd, err := client.Currency.Query().Where().All(ctx)
	if err != nil || len(usd) != 2 {
		t.Fatalf("currencies 期望 2 行: %v %d", err, len(usd))
	}
	var usdRow *ent.Currency
	for _, c := range usd {
		if c.Code == "USD" {
			usdRow = c
		}
	}
	if usdRow == nil || usdRow.Position != "suffix" || usdRow.Rate < 0.139 || usdRow.Rate > 0.141 {
		t.Fatalf("USD 映射异常: %+v", usdRow)
	}

	// user_groups → member_levels（discount 百分数→万分比）+ UserGroup 兼容表
	lvs, _ := client.MemberLevel.Query().All(ctx)
	if len(lvs) != 2 {
		t.Fatalf("member_levels 期望 2 行，实际 %d", len(lvs))
	}
	for _, lv := range lvs {
		if lv.Name == "普通会员" && lv.Discount != 10000 {
			t.Fatalf("默认组 discount 期望 10000，实际 %d", lv.Discount)
		}
		if lv.Name == "黄金会员" {
			if lv.Discount != 8000 || lv.ThresholdType != "both_or" || lv.ThresholdRecharge != 100000 || lv.ThresholdConsume != 500000 {
				t.Fatalf("黄金组映射异常: %+v", lv)
			}
		}
	}
	ugs, _ := client.UserGroup.Query().All(ctx)
	if len(ugs) != 2 {
		t.Fatalf("UserGroup 兼容表期望 2 行，实际 %d", len(ugs))
	}

	// settings：命中映射 + 规范化 + v1_legacy + SECRET 跳过
	get := func(g, k string) *ent.Setting {
		s, err := client.Setting.Query().Where(setting.Group(g), setting.Key(k)).Only(ctx)
		if err != nil {
			return nil
		}
		return s
	}
	if s := get("site", "name"); s == nil || string(s.Value) != `"我的发卡店"` {
		t.Fatalf("site.name 异常: %v", s)
	}
	if s := get("ops", "maintenance"); s == nil || string(s.Value) != `false` {
		t.Fatalf("maintenance_mode 字符串化 bool 未规范化: %v", s)
	}
	if s := get("trade", "order_ttl_minutes"); s == nil || string(s.Value) != `30` {
		t.Fatalf("order_ttl_minutes 异常: %v", s)
	}
	if s := get("trade", "contact_required"); s == nil || string(s.Value) != `"email"` {
		t.Fatalf("require_contact 异常: %v", s)
	}
	if s := get("v1_legacy", "some_unknown_key"); s == nil || string(s.Value) != `{"a":1}` {
		t.Fatalf("v1_legacy 保真异常: %v", s)
	}
	if n, _ := client.Setting.Query().Where(setting.Group("notify")).Count(ctx); n != 0 {
		t.Fatal("mail_password 不应迁入 2.0")
	}

	// users：状态映射 / 快照 / 密码直迁
	users, _ := client.User.Query().All(ctx)
	if len(users) != 7 {
		t.Fatalf("users 期望 7 行，实际 %d", len(users))
	}
	byName := map[string]*ent.User{}
	for _, u := range users {
		byName[u.Username] = u
	}
	if byName["alice"].Status != "banned" {
		t.Fatal("status=0 应映射 banned")
	}
	if byName["carol"].Status != "deleted" {
		t.Fatal("deleted_at 应映射 deleted")
	}
	bob := byName["bob"]
	if bob.Email != "b@x.com" || bob.Phone != "13800000002" {
		t.Fatalf("bob 联系方式异常: %+v", bob)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(bob.PasswordHash), []byte("bob-pass-123")); err != nil {
		t.Fatalf("bcrypt 直迁后无法校验: %v", err)
	}
	wa, err := client.WalletAccount.Query().Where(walletaccount.UserID(bob.ID)).Only(ctx)
	if err != nil || wa.Available != 12000 {
		t.Fatalf("钱包快照异常: %+v err=%v（bob.id=%d）", wa, err, bob.ID)
	}
	pa, err := client.PointAccount.Query().Where(pointaccount.UserID(bob.ID)).Only(ctx)
	if err != nil || pa.Balance != 5 {
		t.Fatalf("积分快照异常: %+v", pa)
	}
	// 时间转换：Asia/Shanghai → UTC（2026-02-01 08:00 +08 = 2026-02-01T00:00Z）
	if !bob.LastLoginAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("last_login_at 时区转换异常: %v", bob.LastLoginAt)
	}

	// 邀请链：dave.l1=bob；eve.l1=dave、eve.l2=bob；frank/grace 环止于一层
	dave, eve := byName["dave"], byName["eve"]
	if dave.InviteL1 != bob.ID || dave.InviteL2 != 0 {
		t.Fatalf("dave 邀请链异常: l1=%d l2=%d（bob=%d）", dave.InviteL1, dave.InviteL2, bob.ID)
	}
	if eve.InviteL1 != dave.ID || eve.InviteL2 != bob.ID {
		t.Fatalf("eve 邀请链异常: l1=%d l2=%d（dave=%d bob=%d）", eve.InviteL1, eve.InviteL2, dave.ID, bob.ID)
	}
	frank := byName["frank"]
	if frank.InviteL1 == 0 || frank.InviteL1 == frank.ID {
		t.Fatalf("环链应止于一层且不指向自身: l1=%d self=%d", frank.InviteL1, frank.ID)
	}

	// admin 提取：bob → admin_users
	adm, err := client.AdminUser.Query().Where(adminuser.Username("bob")).Only(ctx)
	if err != nil {
		t.Fatalf("admin 提取失败: %v", err)
	}
	if adm.PasswordHash != bob.PasswordHash || adm.RoleID == 0 {
		t.Fatalf("admin 映射异常: %+v", adm)
	}

	// supply 凭据：DataBox 重加密可解回 Laravel 明文
	conn, err := client.SupplyConnection.Query().Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox(dataKey)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := box.Open(conn.Credentials, nil)
	if err != nil {
		t.Fatalf("凭据重加密后无法解回: %v", err)
	}
	if err := json.Unmarshal(plain, &map[string]any{}); err != nil {
		t.Fatalf("凭据明文非 JSON: %s", plain)
	}
	if conn.BalanceCache != 5000 || conn.LastSyncedAt.IsZero() {
		t.Fatalf("supply_connection 字段异常: %+v", conn)
	}

	// 幂等重跑：全部 skip、无重复行、settings 不重复
	m2 := runMigrate(t, src, client, appKey, dataKey)
	if got := m2.Stats().Tables["users"]; got.Migrated != 0 || got.SkippedExists != 7 {
		t.Fatalf("重跑应全部幂等跳过: %+v", got)
	}
	n, _ := client.User.Query().Count(ctx)
	if n != 7 {
		t.Fatalf("重跑后用户数异常: %d", n)
	}
	sn, _ := client.Setting.Query().Count(ctx)
	if sn != 5 { // site.name + ops.maintenance + trade×2 + v1_legacy×1
		t.Fatalf("重跑后 settings 数异常: %d", sn)
	}

	// dry-run：只计数不写
	fresh := newTestClient(t)
	rw, _ := NewReportWriter(t.TempDir())
	md := NewMigrator(src, fresh, NewIDMapper(fresh), rw, Options{DryRun: true, Batch: 100}, "Asia/Shanghai")
	md.AppKey, md.DataKey = appKey, dataKey
	if err := md.MigrateSystem(ctx); err != nil {
		t.Fatal(err)
	}
	if err := md.MigrateIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if got := md.Stats().Tables["users"]; got.Planned != 7 || got.Migrated != 0 {
		t.Fatalf("dry-run 计数异常: %+v", got)
	}
	if n, _ := fresh.User.Query().Count(ctx); n != 0 {
		t.Fatal("dry-run 不应写库")
	}
	_ = m
}

// ---------- fixtures/杂项 helpers ----------

type fixFile struct {
	AppKey  string `json:"app_key"`
	Vectors []struct {
		Name    string  `json:"name"`
		Payload string  `json:"payload"`
		Expect  *string `json:"expect"`
	} `json:"vectors"`
}

func loadFixturesForTest(t *testing.T) *fixFile {
	t.Helper()
	raw, err := os.ReadFile("laracrypt/testdata/v1_crypto_fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixFile{}
	if err := json.Unmarshal(raw, f); err != nil {
		t.Fatal(err)
	}
	return f
}

func fixturePayload(t *testing.T, f *fixFile, name string) string {
	t.Helper()
	for _, v := range f.Vectors {
		if v.Name == name {
			return v.Payload
		}
	}
	t.Fatalf("fixtures 缺少向量 %s", name)
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
