package audit

// P2-06 必测项：IPv6 /64 聚合、黑名单（精确+CIDR）、pending 闸门、频率限流、
// 取货失败锁定（TTL/锁定期拒绝）、审计写失败不阻断、访问统计聚合。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newAuditRepo(t *testing.T) (*AuditRepo, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open("file:audittest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewAuditRepo(d, testLogger()), d
}

// TestNormalizeIP IPv6 /64 聚合 + IPv4 原样。
func TestNormalizeIP(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4":                "1.2.3.4",
		"2001:db8:1:2:3:4:5:6":   "2001:db8:1:2::",   // /64 聚合
		"2001:db8:1:2:dead::1":   "2001:db8:1:2::",   // 同 /64 段归一
		"::1":                    "::",               // 回环聚合
		"not-an-ip":              "not-an-ip",        // 非法原样
	}
	for in, want := range cases {
		if got := port.NormalizeIP(in); got != want {
			t.Errorf("NormalizeIP(%q) = %q, want %q", in, got, want)
		}
	}
	// 同 /64 不同后缀 → 归一后相等（闸门判定依据）
	if port.NormalizeIP("2001:db8:9:a:1::1") != port.NormalizeIP("2001:db8:9:a:2::2") {
		t.Fatal("同 /64 段应归一")
	}
}

// TestBlacklist 精确 IP + CIDR 段命中。
func TestBlacklist(t *testing.T) {
	bl := parseBlacklist([]string{
		"1.2.3.4",           // 精确
		"10.20.0.0/16",      // CIDR
		"2001:db8:bad::/48", // IPv6 CIDR（聚合口径）
		"garbage-entry",     // 非法跳过
	})
	if !bl.contains("1.2.3.4") {
		t.Fatal("精确 IP 应命中")
	}
	if !bl.contains("10.20.99.5") {
		t.Fatal("CIDR 段应命中")
	}
	if bl.contains("10.21.0.1") {
		t.Fatal("段外不应命中")
	}
	if !bl.contains("2001:db8:bad::1234") {
		t.Fatal("IPv6 CIDR 段应命中")
	}
	if bl.contains("8.8.8.8") {
		t.Fatal("未列入不应命中")
	}
}

// TestGatePendingBlacklistFreq 闸门三件。
func TestGatePendingBlacklistFreq(t *testing.T) {
	r, d := newAuditRepo(t)
	ctx := context.Background()

	// 1) 黑名单
	r.SetBlacklist([]string{"9.9.9.9"})
	err := r.Check(ctx, port.GateInput{RiskIP: "9.9.9.9"})
	if !errors.Is(err, ErrIPBlacklisted) {
		t.Fatalf("黑名单应拒绝: %v", err)
	}

	// 2) pending 闸门：3 单 pending 后第 4 单拒绝（同 IP）
	for i := 0; i < DefaultMaxPendingPerIP; i++ {
		if _, err := d.Client.Order.Create().
			SetOrderNo(fmt.Sprintf("T-PENDING-%d-%d", time.Now().UnixNano(), i)).
			SetSubsiteID(0).
			SetStatus(order.StatusPendingPayment).
			SetTotalAmount(100).
			SetRiskIP("5.5.5.5").
			SetClientIP("5.5.5.5").
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	err = r.Check(ctx, port.GateInput{RiskIP: "5.5.5.5"})
	if !errors.Is(err, ErrPendingExceed) {
		t.Fatalf("pending 超限应拒绝: %v", err)
	}

	// 3) 频率限流：每 IP 每分钟 N 单
	for i := 0; i < DefaultOrderPerMinPerIP; i++ {
		if err := r.Check(ctx, port.GateInput{RiskIP: "7.7.7.7"}); err != nil {
			t.Fatalf("频率内不应拒绝: %v", err)
		}
	}
	if err := r.Check(ctx, port.GateInput{RiskIP: "7.7.7.7"}); !errors.Is(err, ErrFreqExceed) {
		t.Fatalf("频率超限应拒绝: %v", err)
	}

	// IPv6 /64 聚合：同段不同后缀共享频率窗口
	r2, _ := newAuditRepo(t)
	for i := 0; i < DefaultOrderPerMinPerIP; i++ {
		_ = r2.Check(ctx, port.GateInput{RiskIP: "2001:db8:aa:bb:1::1"})
	}
	if err := r2.Check(ctx, port.GateInput{RiskIP: "2001:db8:aa:bb:2::2"}); !errors.Is(err, ErrFreqExceed) {
		t.Fatalf("同 /64 段应共享闸门: %v", err)
	}
}

// TestFetchLock 取货失败锁定：锁定期内 IsLocked；TTL 过期解锁。
func TestFetchLock(t *testing.T) {
	r, _ := newAuditRepo(t)
	ctx := context.Background()
	key := "fetch:1.2.3.4:T-LOCK-1"

	if locked, _ := r.IsLocked(ctx, key); locked {
		t.Fatal("未锁定前应为未锁")
	}
	if err := r.LockFetchFailure(ctx, key); err != nil {
		t.Fatal(err)
	}
	if locked, _ := r.IsLocked(ctx, key); !locked {
		t.Fatal("锁定后应生效")
	}
	// TTL 过期解锁（直接改库模拟时间流逝）
	_, _ = r.data.DB.ExecContext(ctx, "UPDATE risk_lock_keys SET expires_at = datetime('now', '-1 minute')")
	if locked, _ := r.IsLocked(ctx, key); locked {
		t.Fatal("TTL 过期应解锁")
	}
}

// TestSecurityWriteNoBlock 安全审计写入 + 查询。
func TestSecurityWriteNoBlock(t *testing.T) {
	r, _ := newAuditRepo(t)
	ctx := context.Background()
	r.Security(ctx, port.SecurityEntry{
		ActorType: "admin", ActorID: 1,
		Action: "card.view_content", IP: "3.3.3.3",
		Metadata: map[string]any{"card_id": 42},
	})
	rows, total, err := r.ListSecurityLogs(ctx, "card", 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("安全审计应 1 条: %v %d", err, total)
	}
	if rows[0].IP != "3.3.3.3" {
		t.Fatalf("IP 留档错误: %s", rows[0].IP)
	}
}

// TestVisitCounter 访问统计：内存聚合 → 批量落库。
func TestVisitCounter(t *testing.T) {
	r, _ := newAuditRepo(t)
	ctx := context.Background()
	c := NewVisitCounter()
	c.Bind(r)

	for i := 0; i < 5; i++ {
		c.Record(0, "/api/v1/storefront/products")
	}
	c.Record(0, "/api/v1/storefront/banners")
	c.Flush() // 强制落库

	date := time.Now().UTC().Format("20060102")
	rows, total, err := r.ListVisitStats(ctx, date, 1, 10)
	if err != nil || total != 2 {
		t.Fatalf("统计行应 2 条: %v %d", err, total)
	}
	pv := map[string]int64{}
	for _, v := range rows {
		pv[v.Path] = v.Pv
	}
	if pv["/api/v1/storefront/products"] != 5 {
		t.Fatalf("PV 聚合错误: %+v", pv)
	}
}

// testLogger 静默测试日志。
func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestAlerterThresholdDedup 告警阈值 + 去重窗口。
func TestAlerterThresholdDedup(t *testing.T) {
	var sent []notifyport.Message
	sender := &fakeSender{onSend: func(m notifyport.Message) error { sent = append(sent, m); return nil }}
	// 阈值 3（fetch 维度）
	al := NewAlerter(fakeAlertSettings{raw: []byte(`{"enabled":true,"fetch_fail_per_ip":3,"channel":"telegram"}`)}, sender)

	ctx := context.Background()
	// 2 次：不告警
	al.Count(ctx, "delivery.fetch_failed", "1.2.3.4", "s", "b")
	al.Count(ctx, "delivery.fetch_failed", "1.2.3.4", "s", "b")
	if len(sent) != 0 {
		t.Fatalf("未达阈值不应告警: %d", len(sent))
	}
	// 第 3 次：告警恰一次
	al.Count(ctx, "delivery.fetch_failed", "1.2.3.4", "s", "b")
	if len(sent) != 1 {
		t.Fatalf("达阈值应告警一次: %d", len(sent))
	}
	// 去重窗口内继续计数：不再告警（计数已重置，需再满 3 次且过窗口）
	al.Count(ctx, "delivery.fetch_failed", "1.2.3.4", "s", "b")
	al.Count(ctx, "delivery.fetch_failed", "1.2.3.4", "s", "b")
	al.Count(ctx, "delivery.fetch_failed", "1.2.3.4", "s", "b")
	if len(sent) != 1 {
		t.Fatalf("去重窗口内不应重复告警: %d", len(sent))
	}
	// 不同 IP 独立计数
	al.Count(ctx, "delivery.fetch_failed", "5.6.7.8", "s", "b")
	if len(sent) != 1 {
		t.Fatalf("其他 IP 未达阈值: %d", len(sent))
	}
	// 未配置阈值的维度不计数不告警
	al.Count(ctx, "unknown.action", "1.2.3.4", "s", "b")
	if len(sent) != 1 {
		t.Fatalf("未覆盖维度不应告警: %d", len(sent))
	}
}

// fakeSender 记录告警发送。
type fakeSender struct{ onSend func(notifyport.Message) error }

func (f *fakeSender) Send(_ context.Context, m notifyport.Message) error {
	return f.onSend(m)
}

// fakeAlertSettings 告警配置桩。
type fakeAlertSettings struct{ raw []byte }

func (f fakeAlertSettings) GetJSON(_ context.Context, _, _ string) ([]byte, error) { return f.raw, nil }
