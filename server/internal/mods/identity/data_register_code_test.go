package identity

// 注册验证码测试：通道判定/格式校验/已注册拒绝/冷却/验码一次性/尝试上限。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/emailverification"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newRegCodeEnv(t *testing.T) (*RegisterCodeService, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:regcode%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	sender := &fakeSender{codes: make(chan string, 8)}
	return NewRegisterCodeService(d, sender), d
}

// TestDetectChannel 通道自动判定。
func TestDetectChannel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a@b.com", "email"},
		{"13800138000", "phone"},
		{"hello", ""},
		{"12345", ""},
	}
	for _, c := range cases {
		if got := detectChannel(c.in); got != c.want {
			t.Errorf("detectChannel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSendRegisterCode 发码链路：格式校验/已注册拒绝/落库/冷却。
func TestSendRegisterCode(t *testing.T) {
	svc, d := newRegCodeEnv(t)
	ctx := context.Background()

	// 邮箱格式错误
	if err := svc.SendRegisterCode(ctx, "not-an-email", "email"); err == nil {
		t.Fatal("非法邮箱应拒绝")
	}
	// 手机格式错误
	if err := svc.SendRegisterCode(ctx, "12345", "phone"); err == nil {
		t.Fatal("非法手机号应拒绝")
	}
	// 正常发码（落库）
	if err := svc.SendRegisterCode(ctx, "new@example.com", "email"); err != nil {
		t.Fatalf("发码失败: %v", err)
	}
	cnt, _ := d.Client.EmailVerification.Query().
		Where(emailverification.Email("new@example.com"), emailverification.PurposeEQ(emailverification.PurposeRegister)).
		Count(ctx)
	if cnt != 1 {
		t.Fatalf("验证码未落库: %d", cnt)
	}
	// 60s 冷却
	if err := svc.SendRegisterCode(ctx, "new@example.com", "email"); err == nil {
		t.Fatal("冷却期内重发应拒绝")
	}
	// 已注册邮箱拒绝
	if _, err := d.Client.User.Create().SetUsername("taken").SetEmail("taken@x.com").SetPasswordHash("x").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendRegisterCode(ctx, "taken@x.com", "email"); err == nil {
		t.Fatal("已注册邮箱应拒绝")
	}
}

// TestVerifyRegisterCode 验码：正确通过/一次性/错误计数/上限作废。
func TestVerifyRegisterCode(t *testing.T) {
	svc, d := newRegCodeEnv(t)
	ctx := context.Background()

	// 直接造一条已知码（绕过发送，专注验码逻辑）
	code := "123456"
	_, err := d.Client.EmailVerification.Create().
		SetEmail("v@x.com").
		SetPurpose(emailverification.PurposeRegister).
		SetCodeHash(codeHash(code)).
		SetExpiresAt(time.Now().Add(10 * time.Minute)).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 错码 ×5 → 上限
	for i := 0; i < regCodeMaxTry; i++ {
		if err := svc.VerifyRegisterCode(ctx, "v@x.com", "000000", "email"); err == nil {
			t.Fatal("错码不应通过")
		}
	}
	// 正确码也应被拒（上限作废）
	if err := svc.VerifyRegisterCode(ctx, "v@x.com", code, "email"); err == nil {
		t.Fatal("超限后正确码也应拒绝")
	}

	// 新码：正确通过 + 一次性
	_, err = d.Client.EmailVerification.Create().
		SetEmail("w@x.com").
		SetPurpose(emailverification.PurposeRegister).
		SetCodeHash(codeHash("654321")).
		SetExpiresAt(time.Now().Add(10 * time.Minute)).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyRegisterCode(ctx, "w@x.com", "654321", "email"); err != nil {
		t.Fatalf("正确码应通过: %v", err)
	}
	if err := svc.VerifyRegisterCode(ctx, "w@x.com", "654321", "email"); err == nil {
		t.Fatal("验证码应一次性")
	}
}
