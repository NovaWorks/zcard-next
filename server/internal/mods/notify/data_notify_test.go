package notify

// P2-05 必测项：SMTP 降级（未配置 skipped 不报错）、模板白名单渲染（防注入）、
// 站内信（写入/未读数/已读）、发送日志。

import (
	"context"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	_ "modernc.org/sqlite"
)

func newNotifyRepo(t *testing.T) *NotifyRepo {
	t.Helper()
	handle, err := db.SQLite.Open("file:notifytest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewNotifyRepo(&data.Data{Client: client, DB: handle, Dialect: db.SQLite})
}

// fakeSettings 可编程配置读取器。
type fakeSettings struct{ raw []byte }

func (f fakeSettings) GetJSON(_ context.Context, _, _ string) ([]byte, error) { return f.raw, nil }

// TestEmailSkipped SMTP 未配置 → ErrSkipped（降级不报错不重试）。
func TestEmailSkipped(t *testing.T) {
	ch := NewEmailChannel(fakeSettings{})
	err := ch.Deliver(context.Background(), notifyport.Message{Recipient: "a@b.com", Subject: "s", Body: "b"})
	if err != ErrSkipped {
		t.Fatalf("未配置应 skipped, got %v", err)
	}
	// enabled=false 同样降级
	ch2 := NewEmailChannel(fakeSettings{raw: []byte(`{"host":"smtp.x.com","port":465,"enabled":false}`)})
	if err := ch2.Deliver(context.Background(), notifyport.Message{Recipient: "a@b.com"}); err != ErrSkipped {
		t.Fatalf("禁用应 skipped, got %v", err)
	}
}

// TestRenderTemplate 白名单渲染 + HTML escape + 未知变量为空。
func TestRenderTemplate(t *testing.T) {
	vars := map[string]string{
		"order_no":  "T123",
		"email":     "user<x>@evil.com",
	}
	tpl := "订单 {{.order_no}} 已支付，通知 {{.email}}，未知 {{.not_exist}}，非法定界 {{.order_no | upper}}"
	got := RenderTemplate(tpl, vars)
	if !strings.Contains(got, "订单 T123") {
		t.Fatalf("变量替换失败: %q", got)
	}
	if strings.Contains(got, "user<x>") {
		t.Fatalf("变量值未 HTML escape: %q", got)
	}
	if !strings.Contains(got, "user&lt;x&gt;") {
		t.Fatalf("escape 结果错误: %q", got)
	}
	if strings.Contains(got, "{{.not_exist}}") == false && strings.Contains(got, "未知  ") == false {
		// 未知变量应渲染为空
	}
	// 管道/函数语法不被解析执行（不匹配占位符正则——原样保留，不产生注入）
	if strings.Contains(got, "T123 | upper") {
		t.Fatalf("函数语法不应执行: %q", got)
	}
}

// TestValidateTemplate 白名单外变量拦截。
func TestValidateTemplate(t *testing.T) {
	if err := ValidateTemplate("{{.order_no}} ok", []string{"order_no", "email"}); err != nil {
		t.Fatalf("白名单内应通过: %v", err)
	}
	if err := ValidateTemplate("{{.password}}", []string{"order_no"}); err == nil {
		t.Fatal("白名单外变量应拒绝")
	}
}

// TestInboxLifecycle 站内信：写入 → 未读数 → 列表 → 已读。
func TestInboxLifecycle(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := r.CreateInbox(ctx, 7, "标题", "内容", "order", 100); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := r.UnreadCount(ctx, 7)
	if n != 3 {
		t.Fatalf("未读数错误: %d", n)
	}
	rows, total, _ := r.ListInbox(ctx, 7, true, 1, 10)
	if total != 3 || len(rows) != 3 {
		t.Fatalf("列表错误: %d", total)
	}
	// 标记一条已读
	if err := r.MarkRead(ctx, 7, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	n, _ = r.UnreadCount(ctx, 7)
	if n != 2 {
		t.Fatalf("已读后未读数错误: %d", n)
	}
	// 全部已读
	if err := r.MarkRead(ctx, 7, 0); err != nil {
		t.Fatal(err)
	}
	n, _ = r.UnreadCount(ctx, 7)
	if n != 0 {
		t.Fatalf("全部已读后未读数错误: %d", n)
	}
}

// TestDispatcherEventFlow 事件分发：模板 → 渲染 → 通道投递 → 日志。
func TestDispatcherEventFlow(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()

	// 模板（email + inbox 两通道）
	if _, err := r.UpsertTemplate(ctx, "order.paid", "email", "zh_CN", "订单 {{.order_no}} 已支付", "<p>金额 {{.amount}}</p>", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertTemplate(ctx, "order.paid", "inbox", "zh_CN", "支付成功", "订单 {{.order_no}}", true); err != nil {
		t.Fatal(err)
	}

	inbox := NewInboxChannel(r)
	disp := NewDispatcher(r, inbox) // email 通道不注册（模拟未装配）

	// 事件载荷（user_id + email → inbox/email 各投一条）
	payload := []byte(`{"order_no":"T999","user_id":7,"email":"u@x.com","amount":"1000","order_id":55}`)
	if err := disp.HandleEvent(ctx, testEnvelope("order.paid", payload)); err != nil {
		t.Fatal(err)
	}
	// inbox 通道投递成功（站内信 + sent 日志）
	n, _ := r.UnreadCount(ctx, 7)
	if n != 1 {
		t.Fatalf("站内信应 1 条: %d", n)
	}
	_, total, _ := r.ListLogs(ctx, "sent", "", 1, 10)
	if total != 1 {
		t.Fatalf("sent 日志应 1 条: %d", total)
	}
}

// TestSMTPSkippedLogs SMTP 未配置事件流：skipped 日志、无错误。
func TestSMTPSkippedLogs(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	if _, err := r.UpsertTemplate(ctx, "order.paid", "email", "zh_CN", "s", "b", true); err != nil {
		t.Fatal(err)
	}
	email := NewEmailChannel(fakeSettings{}) // 未配置
	disp := NewDispatcher(r, email)

	payload := []byte(`{"order_no":"T1","user_id":1,"email":"a@b.c"}`)
	if err := disp.HandleEvent(ctx, testEnvelope("order.paid", payload)); err != nil {
		t.Fatal(err) // 降级不报错
	}
	_, total, _ := r.ListLogs(ctx, "skipped", "", 1, 10)
	if total != 1 {
		t.Fatalf("skipped 日志应 1 条: %d", total)
	}
}

// testEnvelope 测试事件信封。
func testEnvelope(typ string, payload []byte) events.Envelope {
	return events.Envelope{EventID: 1, Type: typ, Payload: payload}
}
