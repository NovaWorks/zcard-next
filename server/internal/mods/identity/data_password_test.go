package identity

// 自服务测试：验证码生命周期（冷却/过期/错码递增/超限作废/一次性）、
// 防枚举、改密 session 吊销、重置即登录。

import (
	"context"
	"strings"
	"testing"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/emailverification"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// fakeSender 测试邮件通道（捕获验证码）。
type fakeSender struct{ codes chan string }

func (f *fakeSender) Send(_ context.Context, msg notifyport.Message) error {
	// 从邮件体提取 <b>验证码</b>（六位数字段）
	if i := strings.Index(msg.Body, "20px\">"); i >= 0 {
		seg := msg.Body[i+len("20px\">"):]
		if j := strings.Index(seg, "<"); j >= 0 && len(seg[:j]) == 6 {
			select {
			case f.codes <- seg[:j]:
			default:
			}
		}
	}
	return nil
}

// lastCode 取最近一封邮件验证码。
func (f *fakeSender) lastCode(t *testing.T) string {
	t.Helper()
	select {
	case c := <-f.codes:
		return c
	case <-time.After(time.Second):
		t.Fatal("未捕获验证码邮件")
		return ""
	}
}

func TestForgotPasswordLifecycle(t *testing.T) {
	svc, _ := newUserSvc(t)
	fs := svc.pwd.sender.(*fakeSender)
	ctx := context.Background()

	// 注册带邮箱用户
	if _, err := svc.Register(ctx, &storefrontv1.RegisterRequest{Username: "pwuser", Password: "OldPass123", Email: "pw@t.cn"}); err != nil {
		t.Fatal(err)
	}

	// 1) 防枚举：不存在邮箱同样成功
	if err := svc.pwd.ForgotPassword(ctx, "ghost@t.cn"); err != nil {
		t.Fatalf("防枚举失败：不存在邮箱返回了错误 %v", err)
	}
	select {
	case <-fs.codes:
		t.Fatal("不存在邮箱不应发码")
	default:
	}

	// 2) 正常发码
	if err := svc.pwd.ForgotPassword(ctx, "pw@t.cn"); err != nil {
		t.Fatal(err)
	}
	code := fs.lastCode(t)

	// 3) 60s 冷却
	if err := svc.pwd.ForgotPassword(ctx, "pw@t.cn"); err == nil {
		t.Fatal("冷却期内重复发码应拒绝")
	}

	// 4) 错码递增 attempt；5 次后作废
	for i := 0; i < resetCodeMaxTry; i++ {
		if _, err := svc.pwd.ResetPassword(ctx, "pw@t.cn", "000000", "NewPass456"); err == nil {
			t.Fatal("错码不应重置成功")
		}
	}
	// 正确码也已作废（超限）
	if _, err := svc.pwd.ResetPassword(ctx, "pw@t.cn", code, "NewPass456"); err == nil {
		t.Fatal("超限后正确码也应作废")
	}
}

func TestResetPasswordFullFlow(t *testing.T) {
	svc, d := newUserSvc(t)
	fs := svc.pwd.sender.(*fakeSender)
	ctx := context.Background()

	if _, err := svc.Register(ctx, &storefrontv1.RegisterRequest{Username: "resetu", Password: "OldPass123", Email: "rs@t.cn"}); err != nil {
		t.Fatal(err)
	}
	// 预置一条活跃 session（模拟改密踢线）
	if _, err := d.Client.Session.Create().
		SetRealm(session.RealmUser).SetUserID(1).
		SetRefreshTokenHash("hash-1").SetExpiresAt(time.Now().Add(time.Hour)).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	if err := svc.pwd.ForgotPassword(ctx, "rs@t.cn"); err != nil {
		t.Fatal(err)
	}
	code := fs.lastCode(t)

	// 过期路径：直接把记录改过期
	if _, err := d.Client.EmailVerification.Update().
		Where(emailverification.PurposeEQ(emailverification.PurposeReset)).
		SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pwd.ResetPassword(ctx, "rs@t.cn", code, "NewPass456"); err == nil {
		t.Fatal("过期码应拒绝")
	}

	// 重发（置 verified 作废旧码——冷却查询只看未验证记录，即可发新码）
	if _, err := d.Client.EmailVerification.Update().
		Where(emailverification.PurposeEQ(emailverification.PurposeReset), emailverification.VerifiedAtIsNil()).
		SetVerifiedAt(time.Now()).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.pwd.ForgotPassword(ctx, "rs@t.cn"); err != nil {
		t.Fatal(err)
	}
	code2 := fs.lastCode(t)

	// 正确重置
	if _, err := svc.pwd.ResetPassword(ctx, "rs@t.cn", code2, "NewPass456"); err != nil {
		t.Fatalf("重置失败: %v", err)
	}

	// 验证码一次性：复用同码再试 → 拒绝
	if _, err := svc.pwd.ResetPassword(ctx, "rs@t.cn", code2, "Again789"); err == nil {
		t.Fatal("验证码应一次性")
	}

	// 旧密码失效、新密码可登录
	if _, err := svc.Login(ctx, &storefrontv1.LoginRequest{Username: "resetu", Password: "OldPass123"}); err == nil {
		t.Fatal("旧密码应失效")
	}
	if _, err := svc.Login(ctx, &storefrontv1.LoginRequest{Username: "resetu", Password: "NewPass456"}); err != nil {
		t.Fatalf("新密码登录失败: %v", err)
	}

	// session 已吊销
	n, _ := d.Client.Session.Query().Where(session.RevokedAtNotNil()).Count(ctx)
	if n != 1 {
		t.Fatalf("改密应吊销 session：%d", n)
	}
}

func TestChangePasswordAndProfile(t *testing.T) {
	svc, d := newUserSvc(t)
	ctx := context.Background()
	reg, err := svc.Register(ctx, &storefrontv1.RegisterRequest{Username: "chgu", Password: "OldPass123", Email: "cg@t.cn"})
	_ = reg
	if err != nil {
		t.Fatal(err)
	}
	// 旧密码错 → 拒绝
	if _, err := svc.pwd.ChangePassword(ctx, reg.GetUserId(), "WrongOld", "NewPass456"); err == nil {
		t.Fatal("旧密码错误应拒绝")
	}
	// 正确改密
	if _, err := svc.pwd.ChangePassword(ctx, reg.GetUserId(), "OldPass123", "NewPass456"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, &storefrontv1.LoginRequest{Username: "chgu", Password: "NewPass456"}); err != nil {
		t.Fatal("改密后新密码应可登录")
	}

	// 改邮箱：非法 → 拒绝；合法 → 生效
	if err := svc.pwd.UpdateProfile(ctx, reg.GetUserId(), "not-an-email"); err == nil {
		t.Fatal("非法邮箱应拒绝")
	}
	if err := svc.pwd.UpdateProfile(ctx, reg.GetUserId(), "new@t.cn"); err != nil {
		t.Fatal(err)
	}
	u, _ := d.Client.User.Get(ctx, reg.GetUserId())
	if u.Email != "new@t.cn" {
		t.Fatalf("邮箱未更新: %s", u.Email)
	}
}
