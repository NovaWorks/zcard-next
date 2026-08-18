package authz

// 角色管理与员工管理 API（P0-03 T2；薄 transport，业务在 biz/仓储）。
// 员工数据经 identity/port.AdminMutator 窄接口（跨模块通道 A）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	authzport "github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	identityport "github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// RoleService 角色与权限服务。
type RoleService struct {
	adminv1.UnimplementedRoleServiceServer
	repo RoleRepo
	dir  *Directory
	uc   *RbacUsecase
}

// NewRoleService 构造。
func NewRoleService(repo RoleRepo, dir *Directory, uc *RbacUsecase) *RoleService {
	return &RoleService{repo: repo, dir: dir, uc: uc}
}

// ListRoles 角色列表。
func (s *RoleService) ListRoles(ctx context.Context, _ *emptypb.Empty) (*adminv1.ListRolesReply, error) {
	roles, err := s.repo.Roles(ctx)
	if err != nil {
		return nil, errors.InternalServer("authz.LIST_FAILED", "读取角色失败")
	}
	out := make([]*adminv1.Role, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.PB())
	}
	return &adminv1.ListRolesReply{Roles: out}, nil
}

// GetRole 角色详情（含权限点）。
func (s *RoleService) GetRole(ctx context.Context, req *adminv1.GetRoleRequest) (*adminv1.Role, error) {
	r, err := s.repo.RoleByID(ctx, req.GetId())
	if err != nil {
		return nil, errors.NotFound("authz.ROLE_NOT_FOUND", "角色不存在")
	}
	return r.PB(), nil
}

// CreateRole 创建角色（code 唯一；权限点须在目录内）。
func (s *RoleService) CreateRole(ctx context.Context, req *adminv1.CreateRoleRequest) (*adminv1.Role, error) {
	if err := s.dir.ValidateCodes(req.GetPermissions()); err != nil {
		return nil, errors.BadRequest("authz.INVALID_PERM", err.Error())
	}
	r, err := s.repo.CreateRole(ctx, req.GetName(), req.GetCode(), req.GetDescription(), req.GetPermissions())
	if err != nil {
		return nil, errors.InternalServer("authz.CREATE_FAILED", "创建角色失败（code 可能重复）")
	}
	return r.PB(), nil
}

// UpdateRole 修改角色（内置角色 code 保护）。
func (s *RoleService) UpdateRole(ctx context.Context, req *adminv1.UpdateRoleRequest) (*adminv1.Role, error) {
	r, err := s.repo.UpdateRole(ctx, req.GetId(), req.GetName(), req.GetDescription())
	if err != nil {
		return nil, mapRoleErr(err)
	}
	s.uc.Invalidate(req.GetId())
	return r.PB(), nil
}

// DeleteRole 删除角色（内置拒绝）。
func (s *RoleService) DeleteRole(ctx context.Context, req *adminv1.DeleteRoleRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteRole(ctx, req.GetId()); err != nil {
		return nil, mapRoleErr(err)
	}
	s.uc.Invalidate(req.GetId())
	return &emptypb.Empty{}, nil
}

// UpdateRolePermissions 全量替换权限点（变更实时生效）。
func (s *RoleService) UpdateRolePermissions(ctx context.Context, req *adminv1.UpdateRolePermissionsRequest) (*adminv1.Role, error) {
	if err := s.dir.ValidateCodes(req.GetPermissions()); err != nil {
		return nil, errors.BadRequest("authz.INVALID_PERM", err.Error())
	}
	r, err := s.repo.SetPermissions(ctx, req.GetId(), req.GetPermissions())
	if err != nil {
		return nil, mapRoleErr(err)
	}
	s.uc.Invalidate(req.GetId())
	return r.PB(), nil
}

// GetPermissionTree 权限目录树。
func (s *RoleService) GetPermissionTree(_ context.Context, _ *emptypb.Empty) (*adminv1.PermissionTree, error) {
	tree := s.dir.Tree()
	groups := make([]*adminv1.Group, 0, len(tree))
	for _, g := range tree {
		perms := make([]*adminv1.Perm, 0, len(g.Perms))
		for _, p := range g.Perms {
			perms = append(perms, &adminv1.Perm{Code: p.Code, Desc: p.Desc, AdminOnly: p.AdminOnly, Public: p.Public})
		}
		groups = append(groups, &adminv1.Group{Domain: g.Domain, Label: g.Label, Perms: perms})
	}
	return &adminv1.PermissionTree{Groups: groups}, nil
}

// AdminUserService 员工管理服务（数据经 identity port）。
type AdminUserService struct {
	adminv1.UnimplementedAdminUserServiceServer
	mut     identityport.AdminMutator
	readers identityport.AdminReader
	dir     *Directory
	repo    RoleRepo
}

// NewAdminUserService 构造。
func NewAdminUserService(mut identityport.AdminMutator, readers identityport.AdminReader, dir *Directory, repo RoleRepo) *AdminUserService {
	return &AdminUserService{mut: mut, readers: readers, dir: dir, repo: repo}
}

// ListAdmins 员工列表（附角色名）。
func (s *AdminUserService) ListAdmins(ctx context.Context, _ *emptypb.Empty) (*adminv1.ListAdminsReply, error) {
	accounts, err := s.mut.List(ctx)
	if err != nil {
		return nil, errors.InternalServer("identity.LIST_FAILED", "读取员工失败")
	}
	out := make([]*adminv1.Admin, 0, len(accounts))
	for _, a := range accounts {
		roleName := ""
		if r, err := s.repo.RoleByID(ctx, a.RoleID); err == nil {
			roleName = r.Name
		}
		out = append(out, &adminv1.Admin{
			Id: a.ID, Username: a.Username, Nickname: a.Nickname, Avatar: a.Avatar,
			RoleId: a.RoleID, RoleName: roleName, Enabled: a.Enabled, TotpEnabled: a.TOTPEnabled,
		})
	}
	return &adminv1.ListAdminsReply{Admins: out}, nil
}

// CreateAdmin 创建员工（密码 ≥8 位安全基线）。
func (s *AdminUserService) CreateAdmin(ctx context.Context, req *adminv1.CreateAdminRequest) (*adminv1.Admin, error) {
	if len(req.GetPassword()) < 8 {
		return nil, errors.BadRequest("identity.WEAK_PASSWORD", "密码至少 8 位（安全基线）")
	}
	a, err := s.mut.Create(ctx, identityport.AdminInput{
		Username: req.GetUsername(), Password: req.GetPassword(),
		Nickname: req.GetNickname(), RoleID: req.GetRoleId(), Remark: req.GetRemark(),
	})
	if err != nil {
		return nil, errors.InternalServer("identity.CREATE_FAILED", "创建员工失败（用户名可能重复）")
	}
	return &adminv1.Admin{Id: a.ID, Username: a.Username, Nickname: a.Nickname, RoleId: a.RoleID, Enabled: true}, nil
}

// UpdateAdmin 修改员工。
func (s *AdminUserService) UpdateAdmin(ctx context.Context, req *adminv1.UpdateAdminRequest) (*adminv1.Admin, error) {
	a, err := s.mut.Update(ctx, req.GetId(), identityport.AdminInput{
		Nickname: req.GetNickname(), Avatar: req.GetAvatar(), RoleID: req.GetRoleId(), Remark: req.GetRemark(),
	})
	if err != nil {
		return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
	}
	return &adminv1.Admin{Id: a.ID, Username: a.Username, Nickname: a.Nickname, Avatar: a.Avatar, RoleId: a.RoleID, Enabled: a.Enabled, TotpEnabled: a.TOTPEnabled}, nil
}

// ToggleAdmin 启停员工（禁用即时生效——admin 鉴权中间件每请求回查 enabled；
// 禁用最后一位启用的内置超管被拒，防全员锁死后台）。
func (s *AdminUserService) ToggleAdmin(ctx context.Context, req *adminv1.ToggleAdminRequest) (*adminv1.Admin, error) {
	enabled := req.GetEnabled()
	if !enabled && s.isLastEnabledSuper(ctx, req.GetId()) {
		return nil, errors.Forbidden("identity.LAST_SUPER_ADMIN", "不能禁用最后一位启用的超级管理员")
	}
	a, err := s.mut.Update(ctx, req.GetId(), identityport.AdminInput{Enabled: &enabled})
	if err != nil {
		return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
	}
	return &adminv1.Admin{Id: a.ID, Username: a.Username, Nickname: a.Nickname, RoleId: a.RoleID, Enabled: a.Enabled}, nil
}

// DeleteAdmin 删除员工（内置超管角色与本人不可删；其管理面会话同事务清除）。
func (s *AdminUserService) DeleteAdmin(ctx context.Context, req *adminv1.DeleteAdminRequest) (*emptypb.Empty, error) {
	if claims := identity.ClaimsFromContext(ctx); claims != nil && claims.Subject == req.GetId() {
		return nil, errors.Forbidden("identity.ADMIN_SELF_DELETE", "不能删除当前登录账号")
	}
	if acc, err := s.readers.Admin(ctx, req.GetId()); err == nil && acc != nil && s.repo != nil {
		if role, err := s.repo.RoleByID(ctx, acc.RoleID); err == nil && role.Code == authzport.RoleSuperAdmin {
			return nil, errors.Forbidden("identity.ADMIN_PROTECTED", "内置超级管理员不可删除")
		}
	}
	if err := s.mut.Delete(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
	}
	return &emptypb.Empty{}, nil
}

// isLastEnabledSuper 目标是否为最后一位启用的内置超管（防禁用后无人可管）。
func (s *AdminUserService) isLastEnabledSuper(ctx context.Context, id uint64) bool {
	if s.repo == nil {
		return false
	}
	acc, err := s.readers.Admin(ctx, id)
	if err != nil || acc == nil {
		return false
	}
	role, err := s.repo.RoleByID(ctx, acc.RoleID)
	if err != nil || role.Code != authzport.RoleSuperAdmin {
		return false
	}
	list, err := s.mut.List(ctx)
	if err != nil {
		return false // 查询失败保守放行，由启用状态自身兜底
	}
	for _, a := range list {
		if a.ID != acc.ID && a.Enabled && a.RoleID == acc.RoleID {
			return false
		}
	}
	return true
}

// ResetAdminPassword 重置员工密码（≥8 位安全基线；凭据接管类操作——吊销其全部
// 管理面会话强制重登，防旧会话在密码泄露场景下继续存活）。
func (s *AdminUserService) ResetAdminPassword(ctx context.Context, req *adminv1.ResetAdminPasswordRequest) (*adminv1.Admin, error) {
	if len(req.GetPassword()) < 8 {
		return nil, errors.BadRequest("identity.WEAK_PASSWORD", "密码至少 8 位（安全基线）")
	}
	if err := s.mut.ResetPassword(ctx, req.GetId(), req.GetPassword()); err != nil {
		return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
	}
	_ = s.mut.RevokeAdminSessions(ctx, req.GetId())
	return s.adminByID(ctx, req.GetId())
}

// ResetAdminTOTP 解绑员工 TOTP（二因素移除属高危——吊销其全部管理面会话强制重登，
// 员工下次登录自行重新绑定）。
func (s *AdminUserService) ResetAdminTOTP(ctx context.Context, req *adminv1.ResetAdminTOTPRequest) (*adminv1.Admin, error) {
	if err := s.mut.ClearTOTP(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
	}
	_ = s.mut.RevokeAdminSessions(ctx, req.GetId())
	return s.adminByID(ctx, req.GetId())
}

// adminByID 回读员工最新状态（含角色名）。
func (s *AdminUserService) adminByID(ctx context.Context, id uint64) (*adminv1.Admin, error) {
	a, err := s.readers.Admin(ctx, id)
	if err != nil || a == nil {
		return nil, errors.NotFound("identity.ADMIN_NOT_FOUND", "员工不存在")
	}
	roleName := ""
	if s.repo != nil {
		if r, err := s.repo.RoleByID(ctx, a.RoleID); err == nil {
			roleName = r.Name
		}
	}
	return &adminv1.Admin{
		Id: a.ID, Username: a.Username, Nickname: a.Nickname, Avatar: a.Avatar,
		RoleId: a.RoleID, RoleName: roleName, Enabled: a.Enabled, TotpEnabled: a.TOTPEnabled,
	}, nil
}

func mapRoleErr(err error) error {
	switch {
	case errors.Is(err, ErrRoleNotFound):
		return errors.NotFound("authz.ROLE_NOT_FOUND", "角色不存在")
	case errors.Is(err, ErrRoleBuiltIn):
		return errors.Forbidden("authz.ROLE_BUILTIN", "内置角色受保护")
	case errors.Is(err, ErrRoleInUse):
		return errors.Conflict("authz.ROLE_IN_USE", "角色仍有员工挂载")
	default:
		return errors.InternalServer("authz.ROLE_OP_FAILED", "角色操作失败")
	}
}
