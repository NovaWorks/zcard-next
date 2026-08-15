package authz

// 角色/权限仓储（P0-03）：只读鉴权组 + 管理组 CRUD + 内置角色种子。

import (
	"context"
	"errors"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminrole"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rolepermission"
)

// 角色错误（service 层映射 reason）。
var (
	ErrRoleNotFound = errors.New("authz: 角色不存在")
	ErrRoleBuiltIn  = errors.New("authz: 内置角色受保护")
	ErrRoleInUse    = errors.New("authz: 角色仍有员工挂载")
)

// RoleRepoImpl 角色仓储实现。
type RoleRepoImpl struct {
	data *data.Data
}

// NewRoleRepoImpl 构造。
func NewRoleRepoImpl(d *data.Data) *RoleRepoImpl { return &RoleRepoImpl{data: d} }

// RoleDetail 角色详情（含权限点集）。
type RoleDetail struct {
	ID          uint64
	Name        string
	Code        string
	Description string
	IsBuiltin   bool
	Permissions []string
}

// PB 转协议对象。
func (r *RoleDetail) PB() *adminv1.Role {
	return &adminv1.Role{
		Id: r.ID, Name: r.Name, Code: r.Code, Description: r.Description,
		IsBuiltin: r.IsBuiltin, Permissions: r.Permissions,
	}
}

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

// Roles 全部角色（含权限点集）。
func (r *RoleRepoImpl) Roles(ctx context.Context) ([]*RoleDetail, error) {
	rows, err := data.Client(ctx, r.data).AdminRole.Query().Order(ent.Asc(adminrole.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*RoleDetail, 0, len(rows))
	for _, row := range rows {
		d := &RoleDetail{ID: row.ID, Name: row.Name, Code: row.Code, Description: row.Description, IsBuiltin: row.IsBuiltin}
		if d.Permissions, err = r.PermissionCodes(ctx, row.ID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// RoleByID 角色详情。
func (r *RoleRepoImpl) RoleByID(ctx context.Context, id uint64) (*RoleDetail, error) {
	row, err := data.Client(ctx, r.data).AdminRole.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &RoleDetail{ID: row.ID, Name: row.Name, Code: row.Code, Description: row.Description, IsBuiltin: row.IsBuiltin}
	if d.Permissions, err = r.PermissionCodes(ctx, row.ID); err != nil {
		return nil, err
	}
	return d, nil
}

// CreateRole 创建角色 + 权限点集。
func (r *RoleRepoImpl) CreateRole(ctx context.Context, name, code, desc string, perms []string) (*RoleDetail, error) {
	client := data.Client(ctx, r.data)
	role, err := client.AdminRole.Create().SetName(name).SetCode(code).SetDescription(desc).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.replaceAll(ctx, role.ID, perms); err != nil {
		return nil, err
	}
	return r.RoleByID(ctx, role.ID)
}

// UpdateRole 修改名称/描述（内置角色同样可改名称，code 不可改——接口不暴露 code 字段）。
func (r *RoleRepoImpl) UpdateRole(ctx context.Context, id uint64, name, desc string) (*RoleDetail, error) {
	q := data.Client(ctx, r.data).AdminRole.UpdateOneID(id)
	if name != "" {
		q.SetName(name)
	}
	if desc != "" {
		q.SetDescription(desc)
	}
	if _, err := q.Save(ctx); err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	return r.RoleByID(ctx, id)
}

// DeleteRole 删除（内置拒绝；有员工挂载拒绝；级联删权限点行）。
func (r *RoleRepoImpl) DeleteRole(ctx context.Context, id uint64) error {
	client := data.Client(ctx, r.data)
	role, err := client.AdminRole.Get(ctx, id)
	if ent.IsNotFound(err) {
		return ErrRoleNotFound
	}
	if err != nil {
		return err
	}
	if role.IsBuiltin {
		return ErrRoleBuiltIn
	}
	inUse, err := client.AdminUser.Query().Where(adminuser.RoleID(id)).Exist(ctx)
	if err != nil {
		return err
	}
	if inUse {
		return ErrRoleInUse
	}
	if _, err := client.RolePermission.Delete().Where(rolepermission.RoleID(id)).Exec(ctx); err != nil {
		return err
	}
	return client.AdminRole.DeleteOneID(id).Exec(ctx)
}

// SetPermissions 全量替换权限点（事务内删旧插新）。
func (r *RoleRepoImpl) SetPermissions(ctx context.Context, id uint64, perms []string) (*RoleDetail, error) {
	client := data.Client(ctx, r.data)
	if _, err := client.AdminRole.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	if err := r.replaceAll(ctx, id, perms); err != nil {
		return nil, err
	}
	return r.RoleByID(ctx, id)
}

func (r *RoleRepoImpl) replaceAll(ctx context.Context, roleID uint64, perms []string) error {
	client := data.Client(ctx, r.data)
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if _, err := client.RolePermission.Delete().Where(rolepermission.RoleID(roleID)).Exec(ctx); err != nil {
			return err
		}
		bulk := make([]*ent.RolePermissionCreate, 0, len(perms))
		for _, p := range perms {
			bulk = append(bulk, client.RolePermission.Create().SetRoleID(roleID).SetPermissionCode(p))
		}
		if len(bulk) > 0 {
			return client.RolePermission.CreateBulk(bulk...).Exec(ctx)
		}
		return nil
	})
}

// EnsureBuiltinRoles 落内置角色种子（幂等；P0-03 起 operator/support 覆盖 authz 只读
// 与员工只读两个基础权限点，rbac_coverage_test 据此断言目录覆盖）。
func EnsureBuiltinRoles(ctx context.Context, client *ent.Client) error {
	builtin := []struct{ code, name, desc string }{
		{code: "super_admin", name: "超级管理员", desc: "全部权限（* 通配）"},
		{code: "operator", name: "运营", desc: "商品/订单/设置读"},
		{code: "support", name: "客服", desc: "工单/订单只读"},
	}
	operatorPerms := []string{
		"auth:profile", "auth:logout",
		"settings:read", "settings:read_detail", "settings:update",
		"authz:role_read", "authz:role_read_detail", "authz:tree",
		"identity:admin_read",
	}
	for _, b := range builtin {
		role, err := client.AdminRole.Query().Where(adminrole.Code(b.code)).Only(ctx)
		if ent.IsNotFound(err) {
			role, err = client.AdminRole.Create().
				SetName(b.name).SetCode(b.code).SetDescription(b.desc).SetIsBuiltin(true).Save(ctx)
		}
		if err != nil {
			return err
		}
		var perms []string
		if b.code == "super_admin" {
			perms = []string{"*"}
		} else {
			perms = operatorPerms
		}
		if err := syncRolePerms(ctx, client, role.ID, perms); err != nil {
			return err
		}
	}
	return nil
}

// syncRolePerms 种子权限同步（全量替换；运营角色权限目录增长时随发布更新）。
func syncRolePerms(ctx context.Context, client *ent.Client, roleID uint64, perms []string) error {
	if _, err := client.RolePermission.Delete().Where(rolepermission.RoleID(roleID)).Exec(ctx); err != nil {
		return err
	}
	bulk := make([]*ent.RolePermissionCreate, 0, len(perms))
	for _, p := range perms {
		bulk = append(bulk, client.RolePermission.Create().SetRoleID(roleID).SetPermissionCode(p))
	}
	if len(bulk) > 0 {
		return client.RolePermission.CreateBulk(bulk...).Exec(ctx)
	}
	return nil
}
