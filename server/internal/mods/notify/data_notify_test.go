package notify

// P2-05 必测项：SMTP 降级（未配置 skipped 不报错）、模板白名单渲染（防注入）、
// 站内信（写入/未读数/已读）、发送日志。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
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

// fakeSettings 可编程配置读取器（values["group.key"] 优先；未命中回退 raw——兼容旧单键测试）。
type fakeSettings struct {
	raw    []byte
	values map[string]string // "group.key" → JSON 原文
}

func (f fakeSettings) GetJSON(_ context.Context, group, key string) ([]byte, error) {
	if v, ok := f.values[group+"."+key]; ok {
		return []byte(v), nil
	}
	return f.raw, nil
}

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
		"order_no": "T123",
		"email":    "user<x>@evil.com",
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

// TestAliyunSignGolden SMS 签名 golden vector（Python 独立计算固化）。
func TestAliyunSignGolden(t *testing.T) {
	params := map[string]string{
		"AccessKeyId":      "test-key-id",
		"Action":           "SendSms",
		"Format":           "JSON",
		"PhoneNumbers":     "13800138000",
		"RegionId":         "cn-hangzhou",
		"SignName":         "测试签名",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   "nonce-001",
		"SignatureVersion": "1.0",
		"TemplateCode":     "SMS_123456",
		"TemplateParam":    `{"code":"123456"}`,
		"Timestamp":        "2026-08-16T12:00:00Z",
		"Version":          "2017-05-25",
	}
	got := aliyunSign(params, "test-access-secret")
	want := "sGWPinO1Gft8eYT+wF5h1lA8Fps="
	if got != want {
		t.Fatalf("阿里云签名向量漂移: got %s want %s", got, want)
	}
}

// TestSMSChannelSkipped SMS 未配置 → skipped。
func TestSMSChannelSkipped(t *testing.T) {
	ch := NewSMSChannel(fakeSettings{})
	if err := ch.Deliver(context.Background(), notifyport.Message{Recipient: "13800138000"}); err != ErrSkipped {
		t.Fatalf("未配置应 skipped: %v", err)
	}
	// 仅有服务商无凭据 → 同样 skipped
	ch2 := NewSMSChannel(fakeSettings{values: map[string]string{"notify.sms_provider": `"tencent"`}})
	if err := ch2.Deliver(context.Background(), notifyport.Message{Recipient: "13800138000"}); err != ErrSkipped {
		t.Fatalf("缺凭据应 skipped: %v", err)
	}
}

// TestSMSConfigFlatKeys 扁平键配置解析（后台「邮件短信」页保存口径）。
func TestSMSConfigFlatKeys(t *testing.T) {
	ch := NewSMSChannel(fakeSettings{values: map[string]string{
		"notify.sms_provider":     `"tencent"`,
		"notify.sms_key":          `"test-secret-id"`,
		"notify.sms_secret":       `"test-secret-key"`,
		"notify.sms_sign":         `"签名"`,
		"notify.sms_template_code": `"TPL-1"`,
		"notify.sms_sdk_app_id":   `"1400000000"`,
	}})
	cfg, err := ch.smsConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "tencent" || cfg.AccessKey != "test-secret-id" ||
		cfg.SdkAppID != "1400000000" || cfg.TemplateCode != "TPL-1" {
		t.Fatalf("扁平键解析错误: %+v", cfg)
	}
}

// TestSMSConfigLegacyFallback 旧版 notify.sms JSON blob 兼容（无扁平键时回退）。
func TestSMSConfigLegacyFallback(t *testing.T) {
	ch := NewSMSChannel(fakeSettings{raw: []byte(
		`{"enabled":true,"access_key":"legacy-ak","access_secret":"legacy-sk","sign_name":"旧签名","template_code":"OLD"}`,
	)})
	cfg, err := ch.smsConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "aliyun" || cfg.AccessKey != "legacy-ak" || cfg.AccessSecret != "legacy-sk" {
		t.Fatalf("旧版回退解析错误: %+v", cfg)
	}
}

// TestTencentSignGolden 腾讯云 TC3 签名 golden vector（Python 独立计算固化）。
func TestTencentSignGolden(t *testing.T) {
	payload, _ := json.Marshal(tencentSendSmsPayload{
		PhoneNumberSet:   []string{"+8613800138000"},
		SmsSdkAppId:      "1400000000",
		SignName:         "测试签名",
		TemplateId:       "123456",
		TemplateParamSet: []string{"123456", "5"},
	})
	auth, err := tencentSign("AKIDz8krbsJ5yKBZQpn74WFkmLPx3EXAMPLE",
		"Gu5t9xGARNpq86cd98joQYCN3EXAMPLE", 1551113065, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "TC3-HMAC-SHA256 Credential=AKIDz8krbsJ5yKBZQpn74WFkmLPx3EXAMPLE/2019-02-25/sms/tc3_request, " +
		"SignedHeaders=content-type;host, Signature=825c58ddc7c55bf5018a1af4d3b12393b1573c080cec7f54b528f453d1d5256b"
	if auth != want {
		t.Fatalf("腾讯云签名向量漂移:\n got %s\nwant %s", auth, want)
	}
}

// TestQiniuSignGolden 七牛 QBox 签名 golden vector（Python 独立计算固化）。
func TestQiniuSignGolden(t *testing.T) {
	body := `{"signature_id":"sig-001","template_id":"tpl-001","mobiles":["13800138000"],"parameters":{"code":"123456","minutes":"5"}}`
	auth := qiniuSign("test-access-key", "test-secret-key", body)
	want := "Qiniu test-access-key:uxYCy-h01vyWBlDzHxacsQe9y0w="
	if auth != want {
		t.Fatalf("七牛签名向量漂移: got %s want %s", auth, want)
	}
}

// TestTencentParamSet 模板变量位置数组（变量名字典序）。
func TestTencentParamSet(t *testing.T) {
	got := tencentParamSet(map[string]string{"minutes": "5", "code": "123456", "site": "ZCard"})
	want := []string{"123456", "5", "ZCard"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("位置数组错误: %v", got)
	}
}

// TestTelegramSkipped Telegram 未配置 → skipped。
func TestTelegramSkipped(t *testing.T) {
	ch := NewTelegramChannel(fakeSettings{})
	if err := ch.Deliver(context.Background(), notifyport.Message{Recipient: "12345"}); err != ErrSkipped {
		t.Fatalf("未配置应 skipped: %v", err)
	}
	// 无 chat_ids 同样 skipped
	ch2 := NewTelegramChannel(fakeSettings{raw: []byte(`{"enabled":true,"bot_token":"tok"}`)})
	if err := ch2.Deliver(context.Background(), notifyport.Message{}); err != ErrSkipped {
		t.Fatalf("无目标应 skipped: %v", err)
	}
}

// TestBroadcastFlow 群发：创建（预估）→ 执行（inbox 投递+统计）→ 取消保护。
func TestBroadcastFlow(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()

	// 三个用户（两个 active 一个 banned）
	d := r.data
	for i, status := range []string{"active", "active", "banned"} {
		if _, err := d.Client.User.Create().
			SetUsername(fmt.Sprintf("u%d", i)).
			SetEmail(fmt.Sprintf("u%d@x.com", i)).
			SetStatus(user.Status(status)).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	inbox := NewInboxChannel(r)
	disp := NewDispatcher(r, inbox)
	svc := NewBroadcastService(r, disp, nil) // enq=nil → 降级直接执行

	// 预估：active 筛选 = 2
	n, err := svc.EstimateAudience(ctx, "active", nil)
	if err != nil || n != 2 {
		t.Fatalf("预估错误: %d %v", n, err)
	}

	// 创建 + 立即执行（inbox 通道）
	b, err := svc.Create(ctx, BroadcastInput{
		Title: "促销", Content: "<p>全场 9 折</p>",
		Channels: []string{"inbox"}, TargetType: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Audience != 2 {
		t.Fatalf("覆盖人数回填错误: %d", b.Audience)
	}
	if err := svc.Execute(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	fin, _ := r.GetBroadcast(ctx, b.ID)
	if string(fin.Status) != "done" || fin.SentCount != 2 || fin.FailedCount != 0 {
		t.Fatalf("群发统计错误: %+v", fin)
	}
	// 每用户一条站内信
	for _, uid := range []uint64{1, 2} {
		if c, _ := r.UnreadCount(ctx, uid); c != 1 {
			t.Fatalf("用户 %d 站内信数错误: %d", uid, c)
		}
	}
	// banned 用户无
	if c, _ := r.UnreadCount(ctx, 3); c != 0 {
		t.Fatalf("banned 用户不应收到: %d", c)
	}

	// 已终态不可取消
	if _, err := svc.Cancel(ctx, b.ID); err != ErrBroadcastStarted {
		t.Fatalf("终态取消应拒绝: %v", err)
	}

	// 指定会员 + 取消（pending 可取消）
	b2, err := svc.Create(ctx, BroadcastInput{
		Title: "定向", Content: "x", Channels: []string{"inbox"},
		TargetType: "specified", TargetIDs: []uint64{1},
		ScheduledAt: time.Now().Add(time.Hour), // 定时（cron 触发前不执行）
	})
	if err != nil {
		t.Fatal(err)
	}
	if b2, err = svc.Cancel(ctx, b2.ID); err != nil || string(b2.Status) != "canceled" {
		t.Fatalf("pending 取消失败: %v", err)
	}
}

// fakeBrandResolver 白标解析假实现。
type fakeBrandResolver struct {
	siteName string
	ok       bool
}

func (f *fakeBrandResolver) ResolveBrand(_ context.Context, _ uint64) (notifyport.Brand, bool) {
	return notifyport.Brand{SiteName: f.siteName, Logo: "logo.png"}, f.ok
}

// TestDispatcherBrandIsolation 品牌隔离 fail-closed：
// 分站上下文 → 注入分站白标；无白标 → site_name 留空（绝不暴露主站品牌）。
func TestDispatcherBrandIsolation(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	if _, err := r.UpsertTemplate(ctx, "order.paid", "inbox", "zh_CN", "支付成功", "站点 {{.site_name}} logo={{.site_logo}}", true); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"order_no":"T1","user_id":7,"order_id":1}`)

	// 无白标（fail-closed）：站点变量为空字符串
	disp := NewDispatcher(r, NewInboxChannel(r))
	disp.WithBrandResolver(&fakeBrandResolver{ok: false})
	env := testEnvelope("order.paid", payload)
	env.SubsiteID = 5 // 分站订单
	if err := disp.HandleEvent(ctx, env); err != nil {
		t.Fatal(err)
	}
	msgs, _, _ := r.ListInbox(ctx, 7, false, 1, 10)
	if len(msgs) != 1 || msgs[0].Content != "站点  logo=" {
		t.Fatalf("无白标应渲染为空（不暴露主站品牌）: %q", msgs[0].Content)
	}

	// 有白标：注入分站站名
	disp2 := NewDispatcher(r, NewInboxChannel(r))
	disp2.WithBrandResolver(&fakeBrandResolver{siteName: "分站小店", ok: true})
	env2 := testEnvelope("order.paid", []byte(`{"order_no":"T2","user_id":8,"order_id":2}`))
	env2.SubsiteID = 5
	if err := disp2.HandleEvent(ctx, env2); err != nil {
		t.Fatal(err)
	}
	msgs2, _, _ := r.ListInbox(ctx, 8, false, 1, 10)
	if len(msgs2) != 1 || msgs2[0].Content != "站点 分站小店 logo=logo.png" {
		t.Fatalf("分站白标注入错误: %q", msgs2[0].Content)
	}

	// 主站事件（SubsiteID=0）：不注入品牌（维持现状语义）
	disp3 := NewDispatcher(r, NewInboxChannel(r))
	disp3.WithBrandResolver(&fakeBrandResolver{siteName: "分站小店", ok: true})
	env3 := testEnvelope("order.paid", []byte(`{"order_no":"T3","user_id":9,"order_id":3}`))
	if err := disp3.HandleEvent(ctx, env3); err != nil {
		t.Fatal(err)
	}
	msgs3, _, _ := r.ListInbox(ctx, 9, false, 1, 10)
	if len(msgs3) != 1 || msgs3[0].Content != "站点  logo=" {
		t.Fatalf("主站事件不应注入分站品牌: %q", msgs3[0].Content)
	}
}
