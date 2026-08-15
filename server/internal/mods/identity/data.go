package identity

// 员工仓储 Ent 实现（Ent 收口边界内：data.go 允许 import ent，架构测试 §4.10-5）。

import (
	"context"
	"errors"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
)

// ErrAdminUserNotFound 员工不存在。
var ErrAdminUserNotFound = errors.New("identity: 员工不存在")

// AdminUserRepoImpl 员工仓储实现。
type AdminUserRepoImpl struct {
	data *data.Data
}

// NewAdminUserRepoImpl 构造。
func NewAdminUserRepoImpl(d *data.Data) *AdminUserRepoImpl { return &AdminUserRepoImpl{data: d} }

// FindByUsername 按用户名查找（登录热路径，UNIQUE(username) 索引命中）。
func (r *AdminUserRepoImpl) FindByUsername(ctx context.Context, username string) (*AdminUser, error) {
	row, err := data.Client(ctx, r.data).AdminUser.Query().
		Where(adminuser.Username(username)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrAdminUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &AdminUser{
		ID:            row.ID,
		Username:      row.Username,
		PasswordHash:  row.PasswordHash,
		Nickname:      row.Nickname,
		Avatar:        row.Avatar,
		RoleID:        row.RoleID,
		TOTPSecretEnc: row.TotpSecret,
		Enabled:       row.Enabled,
		LastLoginIP:   row.LastLoginIP,
		LastLoginAt:   row.LastLoginAt,
	}, nil
}

// TouchLogin 更新最近登录 IP 与时间（审计辅助）。
func (r *AdminUserRepoImpl) TouchLogin(ctx context.Context, id uint64, ip string, at time.Time) error {
	_, err := data.Client(ctx, r.data).AdminUser.UpdateOneID(id).
		SetLastLoginIP(ip).
		SetLastLoginAt(at).
		Save(ctx)
	return err
}
