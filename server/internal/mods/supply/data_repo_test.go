package supply

// P2-01 数据层测试：凭据加解密（密文/解密失败提示）、连接 CRUD（映射保护）、
// 映射 upsert 幂等、同步任务生命周期（心跳/统计/终态）。

import (
	"context"
	"encoding/json"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newTestBox(t *testing.T) *crypto.Box {
	t.Helper()
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func newTestRepo(t *testing.T) (*SupplyRepoImpl, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open("file:supplytest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewSupplyRepoImpl(d, newTestBox(t)), d
}

func mustConn(t *testing.T, r *SupplyRepoImpl, d *data.Data, name string) *ent.SupplyConnection {
	t.Helper()
	enc, err := r.SealCredentials("zcard", "https://up.example.com", `{"api_key":"k1","api_secret":"s1"}`)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := r.CreateConnection(context.Background(), &ent.SupplyConnection{
		Name:              name,
		Driver:            "zcard",
		BaseURL:           "https://up.example.com",
		Credentials:       enc,
		Status:            supplyconnection.StatusActive,
		RetryIntervals:    "[30,60,300]",
		ExchangeRate:      1,
		PriceRoundingMode: supplyconnection.PriceRoundingModeNone,
		StockMode:         supplyconnection.StockModeReal,
		AutoSyncPrice:     true,
		Settings:          map[string]any{"auto_onshelf": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// TestCredentialsSealOpen 凭据加解密往返 + 密文不落明文。
func TestCredentialsSealOpen(t *testing.T) {
	r, _ := newTestRepo(t)
	conn := mustConn(t, r, nil, "凭据测试")

	plain, err := r.OpenCredentials(conn)
	if err != nil {
		t.Fatalf("OpenCredentials: %v", err)
	}
	var creds map[string]string
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		t.Fatal(err)
	}
	if creds["api_secret"] != "s1" {
		t.Fatalf("凭据解密错误: %+v", creds)
	}
	// 库内必须为密文（明文绝不出现在 credentials 列）
	if string(conn.Credentials) == `{"api_key":"k1","api_secret":"s1"}` {
		t.Fatal("credentials 列出现明文凭据")
	}
}

// TestCredentialsWrongKeyFails 密钥不匹配 → 解密失败（列表降级提示重配）。
func TestCredentialsWrongKeyFails(t *testing.T) {
	r, _ := newTestRepo(t)
	conn := mustConn(t, r, nil, "凭据测试")
	// 换一个 key 的 box 解密 → 必须失败
	other, _ := crypto.NewBox(make([]byte, 32)) // 全零 key，与测试 box 相同…… 用不同 key
	_ = other
	box2, _ := crypto.NewBox([]byte("0123456789abcdef0123456789abcdef"))
	r2 := NewSupplyRepoImpl(&data.Data{}, box2)
	if _, err := r2.OpenCredentials(conn); err == nil {
		t.Fatal("不同密钥解密必须失败")
	}
}

// TestConnectionCRUD 连接增删改查 + 有映射禁止删除。
func TestConnectionCRUD(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, r, d, "上游A")

	got, err := r.GetConnection(ctx, conn.ID)
	if err != nil || got.Name != "上游A" {
		t.Fatalf("GetConnection: %v %v", got, err)
	}

	// 更新（改 base_url 后凭据需重配：AAD 绑定）
	upd := &ent.SupplyConnection{Name: "上游A-改", BaseURL: "https://up2.example.com"}
	updated, err := r.UpdateConnection(ctx, conn.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BaseURL != "https://up2.example.com" {
		t.Fatalf("更新失败: %+v", updated)
	}
	// 旧 AAD 解密失败 → 提示重配
	if _, err := r.OpenCredentials(updated); err == nil {
		t.Fatal("base_url 变更后旧凭据必须解密失败（AAD 绑定）")
	}
	// 重配凭据（写库）；重新读取后解密
	if err := r.UpdateCredentials(ctx, conn.ID, "zcard", "https://up2.example.com", `{"api_key":"k2","api_secret":"s2"}`); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := r.OpenCredentials(fresh)
	if err != nil {
		t.Fatalf("重配后解密失败: %v", err)
	}
	if !contains(plain, "s2") {
		t.Fatalf("重配凭据未生效: %s", plain)
	}

	// 有映射 → 删除拒绝
	_, err = r.UpsertMapping(ctx, &ent.SupplyMapping{
		ConnectionID:    conn.ID,
		UpstreamProduct: "P1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteConnection(ctx, conn.ID); err != ErrHasMappings {
		t.Fatalf("有映射删除应拒绝, got %v", err)
	}
	// 删映射后可删
	ms, _, _ := r.ListMappings(ctx, conn.ID, 1, 10)
	if err := r.DeleteMapping(ctx, ms[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteConnection(ctx, conn.ID); err != nil {
		t.Fatalf("删除连接失败: %v", err)
	}
}

// TestMappingUpsertIdempotent 映射 upsert 幂等（同键两次 = 一条）。
func TestMappingUpsertIdempotent(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, r, d, "幂等测试")

	for i := 0; i < 2; i++ {
		_, err := r.UpsertMapping(ctx, &ent.SupplyMapping{
			ConnectionID:    conn.ID,
			UpstreamProduct: "P1",
			UpstreamSku:     "",
			LocalProductID:  10 + uint64(i),
			UpStock:         int32(5 + i),
			PricingOverride: map[string]any{"last_synced_price": int64(1000 + i)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	ms, total, err := r.ListMappings(ctx, conn.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("upsert 幂等失败: total=%d", total)
	}
	// 后写覆盖前写
	if ms[0].UpStock != 6 || ms[0].LocalProductID != 11 {
		t.Fatalf("upsert 覆盖语义错误: %+v", ms[0])
	}
}

// TestSyncTaskLifecycle 任务生命周期：pending → processing → 心跳统计 → done。
func TestSyncTaskLifecycle(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, r, d, "任务测试")

	task, err := r.CreateSyncTask(ctx, conn.ID, "full", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != supplysynctask.StatusPending {
		t.Fatalf("初始状态应 pending: %s", task.Status)
	}
	if err := r.SetTaskProcessing(ctx, task.ID, 120); err != nil {
		t.Fatal(err)
	}
	cancel, err := r.TouchTask(ctx, task.ID, TaskProgress{
		Stage: "fetching_products", Page: 2,
		Processed: 10, Created: 3, Updated: 7, PriceUpdated: 5, ManualSkipped: 2,
	})
	if err != nil || cancel {
		t.Fatalf("TouchTask: %v cancel=%v", err, cancel)
	}
	// 第二次累加
	_, err = r.TouchTask(ctx, task.ID, TaskProgress{Processed: 5, Hidden: 1})
	if err != nil {
		t.Fatal(err)
	}
	task, _ = r.GetSyncTask(ctx, task.ID)
	if task.ProcessedCount != 15 || task.CreatedCount != 3 || task.HiddenCount != 1 || task.CurrentPage != 2 {
		t.Fatalf("统计累加错误: %+v", task)
	}

	// 取消请求 → Touch 返回 cancel=true
	_, err = r.RequestCancel(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancel, err = r.TouchTask(ctx, task.ID, TaskProgress{Processed: 1})
	if err != nil || !cancel {
		t.Fatalf("取消标志应生效: err=%v cancel=%v", err, cancel)
	}
	if err := r.FinishTask(ctx, task.ID, supplysynctask.StatusCanceled, "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ = r.GetSyncTask(ctx, task.ID)
	if task.Status != supplysynctask.StatusCanceled {
		t.Fatalf("终态错误: %s", task.Status)
	}
}

// TestUpdatePingResult 探活结果累计（ping_history 统计）。
func TestUpdatePingResult(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, r, d, "探活测试")

	for i := 0; i < 3; i++ {
		if err := r.UpdatePingResult(ctx, conn.ID, true, 120, 5000, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.UpdatePingResult(ctx, conn.ID, false, 0, 0, "timeout"); err != nil {
		t.Fatal(err)
	}
	conn, _ = r.GetConnection(ctx, conn.ID)
	// 最后一次探活失败：last_ping_ok=false + last_error 留痕；余额缓存保留
	if conn.LastPingOk || conn.LastError != "timeout" || conn.BalanceCache != 5000 {
		t.Fatalf("探活失败态错误: %+v", conn)
	}
	hist := conn.Settings["ping_history"].(map[string]any)
	if toInt64(hist["ok"]) != 3 || toInt64(hist["fail"]) != 1 || toInt64(hist["total_latency_ms"]) != 360 {
		t.Fatalf("ping_history 统计错误: %+v", hist)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
