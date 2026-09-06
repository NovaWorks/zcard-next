package migratev1

// IDMapper / 阶段解析 / DSN 脱敏单测。IDMapper 用内存 SQLite + ent 建表（同 inventory 测试模式）。

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

var testDBSeq atomic.Int64

func newTestClient(t *testing.T) *ent.Client {
	t.Helper()
	// 每次唯一库名：cache=shared 会同名共享，跨用例会互相污染
	handle, err := db.SQLite.Open(fmt.Sprintf("file:migv1test%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", testDBSeq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestIDMapperRoundTrip(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	m := NewIDMapper(client)

	if _, ok := m.Get(ctx, "users", 42); ok {
		t.Fatal("未写入前不应命中")
	}
	got, err := m.Put(ctx, client, "users", 42, 1001)
	if err != nil || got != 1001 {
		t.Fatalf("Put 失败: %v got=%d", err, got)
	}
	// 幂等重放：换一个 newID 再 Put，应返回既有映射（迁移重跑语义）
	got2, err := m.Put(ctx, client, "users", 42, 9999)
	if err != nil || got2 != 1001 {
		t.Fatalf("幂等重放应返回既有映射 1001，实际 %d err=%v", got2, err)
	}
	// 新 mapper（冷缓存）从 v1id_maps 读回
	m2 := NewIDMapper(client)
	v, ok := m2.Get(ctx, "users", 42)
	if !ok || v != 1001 {
		t.Fatalf("冷缓存读回失败: ok=%v v=%d", ok, v)
	}
	// 不同表互不串扰
	if _, ok := m.Get(ctx, "orders", 42); ok {
		t.Fatal("表隔离失败：users 的映射泄漏到 orders")
	}
}

func TestPhases(t *testing.T) {
	all, err := ParsePhaseSpec("")
	if err != nil || len(all) != len(Phases) {
		t.Fatalf("空 spec 应返回全部阶段: %v %v", all, err)
	}
	ranged, err := ParsePhaseSpec("0-2")
	if err != nil || len(ranged) != 3 {
		t.Fatalf("区间解析异常: %v %v", ranged, err)
	}
	list, err := ParsePhaseSpec("1,3,1")
	if err != nil || len(list) != 2 || list[0] != 1 || list[1] != 3 {
		t.Fatalf("列表去重异常: %v %v", list, err)
	}
	if _, err := ParsePhaseSpec("9"); err == nil {
		t.Fatal("越界阶段应报错")
	}
	if _, err := ParsePhaseSpec("3-1"); err == nil {
		t.Fatal("倒序区间应报错")
	}
	if got := PhaseSummary(ranged); got != "P0 系统与配置 → P1 身份 → P2 目录" {
		t.Fatalf("PhaseSummary 输出异常: %q", got)
	}
}

func TestMaskDSN(t *testing.T) {
	masked := MaskDSN("user:secret@tcp(10.0.0.1:3306)/zcard?charset=utf8mb4")
	if strings.Contains(masked, "secret") || !strings.Contains(masked, "****") {
		t.Fatalf("DSN 脱敏失败: %s", masked)
	}
	if !strings.Contains(masked, "10.0.0.1:3306") || !strings.Contains(masked, "zcard") {
		t.Fatalf("脱敏不应丢失主机与库名: %s", masked)
	}
}

func TestMySQLDSNFromEnv(t *testing.T) {
	dsn := MySQLDSN(map[string]string{
		"DB_HOST": "localhost", "DB_USERNAME": "zc", "DB_PASSWORD": "p@ss:word",
		"DB_DATABASE": "zcard",
	})
	// 密码含冒号等特殊字符必须被正确转义，且 localhost 归一为 127.0.0.1
	if !strings.Contains(dsn, "127.0.0.1:3306") || !strings.Contains(dsn, "zcard") {
		t.Fatalf("DSN 构造异常: %s", dsn)
	}
	masked := MaskDSN(dsn)
	if strings.Contains(masked, "word") {
		t.Fatalf("含特殊字符的密码脱敏失败: %s", masked)
	}
}
