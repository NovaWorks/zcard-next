package authz

// 角色/权限仓储 Ent 实现；内置角色种子（super_admin/operator/support）在
// install 与 admin create 时落库（admincmd），权限目录随路由表自动生成（M1）。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminrole"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rolepermission"
)

// RoleRepoImpl 角色仓储实现。
type RoleRepoImpl struct {
	data *data.Data
}

// NewRoleRepoImpl 构造。
func NewRoleRepoImpl(d *data.Data) *RoleRepoImpl { return &RoleRepoImpl{data: d} }

// PermissionCodes 角色全部权限点（UNIQUE(role_id, permission_code)）。
func (r *RoleRepoImpl) PermissionCodes(ctx context.Context, roleID uint64) ([]string, error) {
	rows, err := data.Client(ctx, r.data).RolePermission.Query().
		Where(rolepermission.RoleID(roleID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		codes = append(codes, row.PermissionCode)
	}
	return codes, nil
}

// Role 角色名。
func (r *RoleRepoImpl) Role(ctx context.Context, roleID uint64) (string, error) {
	row, err := data.Client(ctx, r.data).AdminRole.Query().
		Where(adminrole.ID(roleID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Name, nil
}

// EnsureBuiltinRoles 落内置角色种子（幂等，install 与启动校验复用）。
// 权限目录自动生成 M1 交付；M0 种子仅 super_admin 通配 *，其余角色建而不授权
// （新增路由未挂角色 = 仅超管可见，rbac_coverage_test 保证每条路由被覆盖，§5.14）。
func EnsureBuiltinRoles(ctx context.Context, client *ent.Client) error {
	builtin := []struct{ code, name, perm string }{
		{code: "super_admin", name: "超级管理员", perm: "*"},
		{code: "operator", name: "运营", perm: "settings:read"},
		{code: "support", name: "客服", perm: "settings:read"},
	}
	for _, b := range builtin {
		role, err := client.AdminRole.Query().Where(adminrole.Code(b.code)).Only(ctx)
		if ent.IsNotFound(err) {
			role, err = client.AdminRole.Create().
				SetName(b.name).
				SetCode(b.code).
				SetIsBuiltin(true).
				Save(ctx)
		}
		if err != nil {
			return err
		}
		exists, err := client.RolePermission.Query().
			Where(rolepermission.RoleID(role.ID), rolepermission.PermissionCode(b.perm)).
			Exist(ctx)
		if err != nil {
			return err
		}
		if !exists {
			if err := client.RolePermission.Create().
				SetRoleID(role.ID).
				SetPermissionCode(b.perm).
				Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
