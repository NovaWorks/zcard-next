package authz

// 员工重置类 API 面测试（P0-03 补全：重置密码/解绑 TOTP）。
// service 层用 fake AdminMutator/AdminReader + 真实 Directory——
// 校验参数基线、会话吊销调用次序、目录对新 op 的权限点声明。

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"

	"github.com/go-kratos/kratos/v3/errors"
)

type fakeMutator struct {
	port.AdminMutator // 其余方法不测，嵌入零值即可

	resetPwd     []string // 已重置密码（id:pwd）
	clearedTOTP  []uint64
	revoked      []uint64
	deleted      []uint64
	accounts     []port.AdminAccount
	resetPwdErr  error
	clearTOTPErr error
}

func (f *fakeMutator) List(ctx context.Context) ([]port.AdminAccount, error) { return f.accounts, nil }
func (f *fakeMutator) Delete(ctx context.Context, id uint64) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeMutator) Create(ctx context.Context, in port.AdminInput) (*port.AdminAccount, error) {
	return nil, nil
}
func (f *fakeMutator) Update(ctx context.Context, id uint64, in port.AdminInput) (*port.AdminAccount, error) {
	enabled := in.Enabled == nil || *in.Enabled
	return &port.AdminAccount{ID: id, Enabled: enabled}, nil
}
func (f *fakeMutator) RoleInUse(ctx context.Context, roleID uint64) (bool, error) { return false, nil }
func (f *fakeMutator) ResetPassword(ctx context.Context, id uint64, password string) error {
	if f.resetPwdErr != nil {
		return f.resetPwdErr
	}
	f.resetPwd = append(f.resetPwd, string(rune(id))+" "+password)
	return nil
}
func (f *fakeMutator) ClearTOTP(ctx context.Context, id uint64) error {
	if f.clearTOTPErr != nil {
		return f.clearTOTPErr
	}
	f.clearedTOTP = append(f.clearedTOTP, id)
	return nil
}
func (f *fakeMutator) RevokeAdminSessions(ctx context.Context, id uint64) error {
	f.revoked = append(f.revoked, id)
	return nil
}

type fakeReader struct {
	account *port.AdminAccount
}

func (f fakeReader) Admin(ctx context.Context, id uint64) (*port.AdminAccount, error) {
	if f.account != nil && f.account.ID == id {
		return f.account, nil
	}
	return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
}

// fakeRoleRepo 仅实现 DeleteAdmin/ToggleAdmin 守卫用到的 RoleByID。
type fakeRoleRepo struct {
	RoleRepo // 其余方法不测
	byID     map[uint64]*RoleDetail
}

func (f fakeRoleRepo) RoleByID(ctx context.Context, id uint64) (*RoleDetail, error) {
	if d, ok := f.byID[id]; ok {
		return d, nil
	}
	return nil, ErrRoleNotFound
}

func newAdminSvcForTest(mut *fakeMutator, reader fakeReader) *AdminUserService {
	return NewAdminUserService(mut, reader, NewDirectory(), nil)
}

func newAdminSvcWithRepo(mut *fakeMutator, reader fakeReader, repo RoleRepo) *AdminUserService {
	return NewAdminUserService(mut, reader, NewDirectory(), repo)
}

// TestResetAdminPasswordWeakPassword 弱密码拒绝（<8 位）且不触碰仓储。
func TestResetAdminPasswordWeakPassword(t *testing.T) {
	mut := &fakeMutator{}
	svc := newAdminSvcForTest(mut, fakeReader{})

	_, err := svc.ResetAdminPassword(context.Background(), &adminv1.ResetAdminPasswordRequest{Id: 1, Password: "1234567"})
	if !errors.IsBadRequest(err) {
		t.Fatalf("7 位密码应 BadRequest: %v", err)
	}
	if len(mut.resetPwd) != 0 || len(mut.revoked) != 0 {
		t.Fatal("弱密码不应触发重置/吊销")
	}
}

// TestResetAdminPasswordFlow 合法重置：改密 + 吊销会话 + 回读最新状态。
func TestResetAdminPasswordFlow(t *testing.T) {
	mut := &fakeMutator{}
	reader := fakeReader{account: &port.AdminAccount{ID: 7, Username: "alice", RoleID: 3, Enabled: true}}
	svc := newAdminSvcForTest(mut, reader)

	a, err := svc.ResetAdminPassword(context.Background(), &adminv1.ResetAdminPasswordRequest{Id: 7, Password: "strong-pass-9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mut.resetPwd) != 1 || len(mut.revoked) != 1 || mut.revoked[0] != 7 {
		t.Fatalf("应改密一次并吊销会话: reset=%v revoked=%v", mut.resetPwd, mut.revoked)
	}
	if a.Id != 7 || a.Username != "alice" || a.RoleId != 3 {
		t.Fatalf("回读不符: %+v", a)
	}
}

// TestResetAdminPasswordNotFound 员工不存在：404 且不吊销。
func TestResetAdminPasswordNotFound(t *testing.T) {
	mut := &fakeMutator{resetPwdErr: errors.NotFound("x", "y")}
	svc := newAdminSvcForTest(mut, fakeReader{})

	_, err := svc.ResetAdminPassword(context.Background(), &adminv1.ResetAdminPasswordRequest{Id: 99, Password: "strong-pass-9"})
	if !errors.IsNotFound(err) {
		t.Fatalf("应 NotFound: %v", err)
	}
	if len(mut.revoked) != 0 {
		t.Fatal("员工不存在时不应吊销会话")
	}
}

// TestResetAdminTOTPFlow 解绑 TOTP：清绑定 + 吊销会话 + 状态回读。
func TestResetAdminTOTPFlow(t *testing.T) {
	mut := &fakeMutator{}
	reader := fakeReader{account: &port.AdminAccount{ID: 7, Username: "alice", RoleID: 3, Enabled: true, TOTPEnabled: false}}
	svc := newAdminSvcForTest(mut, reader)

	a, err := svc.ResetAdminTOTP(context.Background(), &adminv1.ResetAdminTOTPRequest{Id: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(mut.clearedTOTP) != 1 || mut.clearedTOTP[0] != 7 {
		t.Fatalf("应解绑一次: %v", mut.clearedTOTP)
	}
	if len(mut.revoked) != 1 || mut.revoked[0] != 7 {
		t.Fatalf("应吊销会话: %v", mut.revoked)
	}
	if a.TotpEnabled {
		t.Fatal("解绑后 totp_enabled 应为 false")
	}
}

// TestResetOpsPermissionDeclared 新 op 的权限点已声明且 AdminOnly（启动对账前置）。
func TestResetOpsPermissionDeclared(t *testing.T) {
	dir := NewDirectory()

	cases := []struct {
		op   string
		code string
	}{
		{"zcard.api.admin.v1.AdminUserService/ResetAdminPassword", "identity:admin_reset_pwd"},
		{"zcard.api.admin.v1.AdminUserService/ResetAdminTOTP", "identity:admin_totp_reset"},
	}
	for _, c := range cases {
		code, _, ok := dir.PermissionForOp(c.op)
		if !ok || code != c.code {
			t.Fatalf("%s 应声明为 %s: got %s ok=%v", c.op, c.code, code, ok)
		}
		p, ok := dir.Perm(code)
		if !ok || !p.AdminOnly {
			t.Fatalf("%s 应为 AdminOnly: %+v", code, p)
		}
	}
}

// TestDeleteAdminSelfForbidden 不能删除当前登录账号。
func TestDeleteAdminSelfForbidden(t *testing.T) {
	mut := &fakeMutator{}
	svc := newAdminSvcForTest(mut, fakeReader{})
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})

	_, err := svc.DeleteAdmin(ctx, &adminv1.DeleteAdminRequest{Id: 7})
	if !errors.IsForbidden(err) {
		t.Fatalf("自删应 Forbidden: %v", err)
	}
	if len(mut.deleted) != 0 {
		t.Fatal("自删不应触达仓储")
	}
}

// TestDeleteAdminSuperProtected 内置超管角色账号不可删除。
func TestDeleteAdminSuperProtected(t *testing.T) {
	mut := &fakeMutator{}
	reader := fakeReader{account: &port.AdminAccount{ID: 1, Username: "admin", RoleID: 9, Enabled: true}}
	repo := fakeRoleRepo{byID: map[uint64]*RoleDetail{9: {ID: 9, Code: "super_admin"}}}
	svc := newAdminSvcWithRepo(mut, reader, repo)

	_, err := svc.DeleteAdmin(context.Background(), &adminv1.DeleteAdminRequest{Id: 1})
	if !errors.IsForbidden(err) {
		t.Fatalf("超管删除应 Forbidden: %v", err)
	}
	if len(mut.deleted) != 0 {
		t.Fatal("超管删除不应触达仓储")
	}
}

// TestDeleteAdminFlow 普通员工删除放行。
func TestDeleteAdminFlow(t *testing.T) {
	mut := &fakeMutator{}
	reader := fakeReader{account: &port.AdminAccount{ID: 5, Username: "staff1", RoleID: 2, Enabled: true}}
	repo := fakeRoleRepo{byID: map[uint64]*RoleDetail{2: {ID: 2, Code: "operator"}}}
	svc := newAdminSvcWithRepo(mut, reader, repo)

	if _, err := svc.DeleteAdmin(context.Background(), &adminv1.DeleteAdminRequest{Id: 5}); err != nil {
		t.Fatal(err)
	}
	if len(mut.deleted) != 1 || mut.deleted[0] != 5 {
		t.Fatalf("应删除一次: %v", mut.deleted)
	}
}

// TestToggleLastSuperGuard 禁用最后一位启用超管被拒；存在其他启用超管则放行。
func TestToggleLastSuperGuard(t *testing.T) {
	reader := fakeReader{account: &port.AdminAccount{ID: 1, Username: "admin", RoleID: 9, Enabled: true}}
	repo := fakeRoleRepo{byID: map[uint64]*RoleDetail{9: {ID: 9, Code: "super_admin"}}}

	// 仅一位启用超管
	mut := &fakeMutator{accounts: []port.AdminAccount{
		{ID: 1, RoleID: 9, Enabled: true},
		{ID: 2, RoleID: 9, Enabled: false},
	}}
	svc := newAdminSvcWithRepo(mut, reader, repo)
	if _, err := svc.ToggleAdmin(context.Background(), &adminv1.ToggleAdminRequest{Id: 1, Enabled: false}); !errors.IsForbidden(err) {
		t.Fatalf("末位启用超管禁用应 Forbidden: %v", err)
	}

	// 存在另一位启用超管 → 放行
	mut2 := &fakeMutator{accounts: []port.AdminAccount{
		{ID: 1, RoleID: 9, Enabled: true},
		{ID: 2, RoleID: 9, Enabled: true},
	}}
	svc2 := newAdminSvcWithRepo(mut2, reader, repo)
	if _, err := svc2.ToggleAdmin(context.Background(), &adminv1.ToggleAdminRequest{Id: 1, Enabled: false}); err != nil {
		t.Fatalf("非末位超管禁用应放行: %v", err)
	}

	// 启用方向永不拦截
	mut3 := &fakeMutator{accounts: []port.AdminAccount{{ID: 1, RoleID: 9, Enabled: false}}}
	svc3 := newAdminSvcWithRepo(mut3, reader, repo)
	if _, err := svc3.ToggleAdmin(context.Background(), &adminv1.ToggleAdminRequest{Id: 1, Enabled: true}); err != nil {
		t.Fatalf("启用方向不应拦截: %v", err)
	}
}
