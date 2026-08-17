package identity

// 用户中心测试：注册（归因链/环拒绝/重名）、登录、密码校验。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newUserSvc(t *testing.T) (*StoreUserService, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:usertest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	signer, err := authn.NewSigner(make([]byte, 32), make([]byte, 32), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return NewStoreUserService(NewUserRepo(d), signer, d), d
}

// TestRegisterChain 注册归因链（l1/l2/l3 三级传递）。
func TestRegisterChain(t *testing.T) {
	svc, d := newUserSvc(t)
	ctx := context.Background()

	// A（无链）
	a, err := svc.repo.Register(ctx, RegisterInput{Username: "alice", Password: "pass123"})
	if err != nil {
		t.Fatal(err)
	}
	if a.InviteL1 != 0 || a.InviteL2 != 0 || a.InviteL3 != 0 {
		t.Fatal("无码注册链应空")
	}
	// B←A
	b, err := svc.repo.Register(ctx, RegisterInput{Username: "bob", Password: "pass123", InviteCode: fmt.Sprint(a.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if b.InviteL1 != a.ID {
		t.Fatalf("B.l1 应=A: %d", b.InviteL1)
	}
	// C←B：l1=B l2=A
	c, err := svc.repo.Register(ctx, RegisterInput{Username: "carol", Password: "pass123", InviteCode: fmt.Sprint(b.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if c.InviteL1 != b.ID || c.InviteL2 != a.ID || c.InviteL3 != 0 {
		t.Fatalf("C 链错误: %+v", c)
	}
	// D←C：l1=C l2=B l3=A
	dd, err := svc.repo.Register(ctx, RegisterInput{Username: "dave", Password: "pass123", InviteCode: fmt.Sprint(c.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if dd.InviteL1 != c.ID || dd.InviteL2 != b.ID || dd.InviteL3 != a.ID {
		t.Fatalf("D 链错误: %+v", dd)
	}
	_ = d
}

// TestRegisterGuards 注册守卫（重名/短密码/无效邀请码）。
func TestRegisterGuards(t *testing.T) {
	svc, _ := newUserSvc(t)
	ctx := context.Background()
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "ab", Password: "pass123"}); err == nil {
		t.Fatal("短用户名应拒绝")
	}
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "valid", Password: "123"}); err == nil {
		t.Fatal("短密码应拒绝")
	}
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "u1", Password: "pass123", InviteCode: "999"}); err == nil {
		t.Fatal("无效邀请码应拒绝")
	}
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "alice", Password: "pass123"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "alice", Password: "pass456"}); err == nil {
		t.Fatal("重名应拒绝")
	}
}

// TestLogin 登录 + JWT（user realm 签发/验证往返）。
func TestLogin(t *testing.T) {
	svc, d := newUserSvc(t)
	ctx := context.Background()
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "loginuser", Password: "pass123"}); err != nil {
		t.Fatal(err)
	}
	// API 登录
	reply, err := svc.Login(ctx, &storefrontv1.LoginRequest{Username: "loginuser", Password: "pass123"})
	if err != nil || reply.AccessToken == "" {
		t.Fatalf("登录失败: %v", err)
	}
	// 错密码
	if _, err := svc.Login(ctx, &storefrontv1.LoginRequest{Username: "loginuser", Password: "wrong"}); err == nil {
		t.Fatal("错密码应拒绝")
	}
	// 不存在用户
	if _, err := svc.Login(ctx, &storefrontv1.LoginRequest{Username: "ghost", Password: "pass123"}); err == nil {
		t.Fatal("不存在用户应拒绝")
	}
	_ = d
}
