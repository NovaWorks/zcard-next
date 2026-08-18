package identity

// 管理员重置类操作测试：ResetAdminPassword / ClearTOTP / RevokeAdminSessions
// （authz API 面消费的 port.AdminMutator 三个方法；真实 sqlite 内存库）。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newAdminResetFixture(t *testing.T) (*AdminUserRepoImpl, *IdentityUsecase, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:adminreset%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
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
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewAdminUserRepoImpl(d)
	return repo, NewIdentityUsecase(repo, signer, d, box), d
}

func seedAdmin(t *testing.T, d *data.Data) uint64 {
	t.Helper()
	hash, err := crypto.HashPassword("oldpass123")
	if err != nil {
		t.Fatal(err)
	}
	u, err := d.Client.AdminUser.Create().
		SetUsername("victim").
		SetPasswordHash(hash).
		SetRoleID(1).
		SetEnabled(true).
		Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func seedSession(t *testing.T, d *data.Data, userID uint64, realm session.Realm) string {
	t.Helper()
	token := fmt.Sprintf("refresh-%d-%s", userID, realm)
	_, err := d.Client.Session.Create().
		SetRealm(realm).
		SetUserID(userID).
		SetRefreshTokenHash(hashToken(token)).
		SetExpiresAt(time.Now().Add(24 * time.Hour)).
		Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// TestResetAdminPasswordFlow 重置密码：旧密失效、新密可登录。
func TestResetAdminPasswordFlow(t *testing.T) {
	repo, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)

	if err := repo.ResetPassword(ctx, id, "newpass456"); err != nil {
		t.Fatal(err)
	}

	if _, err := uc.AdminLogin(ctx, "victim", "oldpass123", "", "1.2.3.4"); err == nil {
		t.Fatal("旧密码应失效")
	}
	res, err := uc.AdminLogin(ctx, "victim", "newpass456", "", "1.2.3.4")
	if err != nil {
		t.Fatalf("新密码应可登录: %v", err)
	}
	if res.Admin.Username != "victim" {
		t.Fatalf("登录用户名不符: %s", res.Admin.Username)
	}

	// 不存在的员工：报错而非静默
	if err := repo.ResetPassword(ctx, 9999, "whatever123"); err == nil {
		t.Fatal("不存在的员工应报错")
	}
}

// TestRevokeAdminSessions 吊销会话：admin realm 全吊销、user realm 不受影响。
func TestRevokeAdminSessions(t *testing.T) {
	_, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)

	adminToken := seedSession(t, d, id, session.RealmAdmin)
	seedSession(t, d, id, session.RealmUser) // user realm 同 UID（模拟撞号场景）

	if err := NewAdminUserRepoImpl(d).RevokeAdminSessions(ctx, id); err != nil {
		t.Fatal(err)
	}

	if _, err := uc.RefreshAccess(ctx, adminToken); err == nil {
		t.Fatal("被吊销的 admin 会话 refresh 应失败")
	}
	// user realm 会话不被动（realm 条件限定）
	sessions, err := d.Client.Session.Query().
		Where(session.UserID(id), session.RealmEQ(session.RealmUser)).
		All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || !sessions[0].RevokedAt.IsZero() {
		t.Fatal("user realm 会话不应被吊销")
	}

	// 幂等：再次吊销不报错
	if err := NewAdminUserRepoImpl(d).RevokeAdminSessions(ctx, id); err != nil {
		t.Fatal(err)
	}
}

// TestClearAdminTOTP 解绑 TOTP：解绑后无需动态码即可登录。
func TestClearAdminTOTP(t *testing.T) {
	repo, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)

	// 绑定 TOTP（与 EnableTOTP 同路径：Seal(totp:%d) AAD）
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte(fmt.Sprintf("totp:%d", id)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTOTPSecret(ctx, id, enc); err != nil {
		t.Fatal(err)
	}

	// 已绑定：无动态码登录被拒（ErrTOTPRequired）
	if _, err := uc.AdminLogin(ctx, "victim", "oldpass123", "", "1.2.3.4"); err != ErrTOTPRequired {
		t.Fatalf("绑定后无码登录应要求 TOTP: %v", err)
	}

	// 解绑（authz API 面路径）
	if err := repo.ClearTOTP(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.AdminLogin(ctx, "victim", "oldpass123", "", "1.2.3.4"); err != nil {
		t.Fatalf("解绑后应免码登录: %v", err)
	}

	// 幂等：未绑定再解绑不报错
	if err := repo.ClearTOTP(ctx, id); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteAdminRemovesSessions 删除员工：本人行与其管理面会话一并清除，user realm 不受影响。
func TestDeleteAdminRemovesSessions(t *testing.T) {
	repo, _, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)
	seedSession(t, d, id, session.RealmAdmin)
	seedSession(t, d, id, session.RealmUser)
	seedSession(t, d, 999, session.RealmAdmin) // 他人会话不受影响

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.AdminUser.Get(ctx, id); !ent.IsNotFound(err) {
		t.Fatal("员工行应已删除")
	}
	adminLeft, err := d.Client.Session.Query().Where(session.UserID(id), session.RealmEQ(session.RealmAdmin)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if adminLeft != 0 {
		t.Fatal("其管理面会话应一并清除")
	}
	userLeft, err := d.Client.Session.Query().Where(session.UserID(id), session.RealmEQ(session.RealmUser)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if userLeft != 1 {
		t.Fatal("user realm 会话不应被动")
	}
	otherLeft, err := d.Client.Session.Query().Where(session.UserID(999)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if otherLeft != 1 {
		t.Fatal("他人会话不应被动")
	}

	// 不存在的员工：报错
	if err := repo.Delete(ctx, 4242); err == nil {
		t.Fatal("删除不存在的员工应报错")
	}
}
