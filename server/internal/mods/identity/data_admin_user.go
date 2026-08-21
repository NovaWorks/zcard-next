package identity

// AdminUserManageService 前台用户管理（列表/详情/封禁解封）。
// 数据域 users 表（storefront 注册用户）；员工管理（admin_users）在 authz 模块。
// 分站隔离：users 表无 subsite 列（全站账户），管理面按全站口径查询。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"

	"github.com/go-kratos/kratos/v3/errors"
)

// AdminUserManageService 实现 adminv1.AdminUserManageService。
type AdminUserManageService struct {
	adminv1.UnimplementedAdminUserManageServiceServer
	repo *UserRepo
}

// NewAdminUserManageService 构造。
func NewAdminUserManageService(repo *UserRepo) *AdminUserManageService {
	return &AdminUserManageService{repo: repo}
}

// ListUsers 用户列表（关键词 username/email 模糊；status 筛选；分页）。
func (s *AdminUserManageService) ListUsers(ctx context.Context, req *adminv1.ListUsersRequest) (*adminv1.ListUsersReply, error) {
	q := data.Client(ctx, s.repo.data).User.Query()
	if kw := req.GetKeyword(); kw != "" {
		q = q.Where(user.Or(
			user.UsernameContainsFold(kw),
			user.EmailContainsFold(kw),
		))
	}
	if st := req.GetStatus(); st != "" {
		if !validUserStatus(st) {
			return nil, errors.BadRequest("identity.INVALID_STATUS", "状态仅支持 active/banned")
		}
		q = q.Where(user.StatusEQ(user.Status(st)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, errors.InternalServer("identity.USER_LIST_FAILED", "读取用户列表失败")
	}
	page, size := normPage(req.GetPage(), req.GetPageSize())
	rows, err := q.
		Order(ent.Desc(user.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, errors.InternalServer("identity.USER_LIST_FAILED", "读取用户列表失败")
	}
	reply := &adminv1.ListUsersReply{Total: int64(total)}
	for _, r := range rows {
		reply.Users = append(reply.Users, toPBUser(r))
	}
	return reply, nil
}

// GetUser 用户详情。
func (s *AdminUserManageService) GetUser(ctx context.Context, req *adminv1.GetUserRequest) (*adminv1.UserItem, error) {
	row, err := data.Client(ctx, s.repo.data).User.Get(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("identity.USER_NOT_FOUND", "用户不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("identity.USER_GET_FAILED", "读取用户失败")
	}
	return toPBUser(row), nil
}

// SetUserStatus 封禁/解封（deleted 不可经此设置）。
func (s *AdminUserManageService) SetUserStatus(ctx context.Context, req *adminv1.SetUserStatusRequest) (*adminv1.UserItem, error) {
	if !validUserStatus(req.GetStatus()) {
		return nil, errors.BadRequest("identity.INVALID_STATUS", "状态仅支持 active/banned")
	}
	row, err := data.Client(ctx, s.repo.data).User.UpdateOneID(req.GetId()).
		SetStatus(user.Status(req.GetStatus())).
		Save(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("identity.USER_NOT_FOUND", "用户不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("identity.USER_STATUS_FAILED", "更新用户状态失败")
	}
	return toPBUser(row), nil
}

func validUserStatus(st string) bool {
	return st == "active" || st == "banned"
}

func normPage(page, size int32) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return int(page), int(size)
}

func toPBUser(r *ent.User) *adminv1.UserItem {
	item := &adminv1.UserItem{
		Id:        r.ID,
		Username:  r.Username,
		Email:     r.Email,
		Status:    string(r.Status),
		CreatedAt: r.CreatedAt.Unix(),
	}
	if !r.LastLoginAt.IsZero() {
		item.LastLoginAt = r.LastLoginAt.Unix()
	}
	return item
}
