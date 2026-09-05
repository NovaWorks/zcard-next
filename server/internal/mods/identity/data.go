package identity

// 员工仓储 Ent 实现（Ent 收口边界内：data.go 允许 import ent，架构测试 -5）。

import (
	"context"
	"errors"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
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

// ── 员工管理（port.AdminMutator 实现，authz API 面消费；）────────────

// Admin 按 ID 取员工账户（port.AdminReader 实现——admin 鉴权中间件每请求
// 校验 enabled 与当前 RoleID，禁用/换角色即时生效）。
func (r *AdminUserRepoImpl) Admin(ctx context.Context, id uint64) (*port.AdminAccount, error) {
	row, err := data.Client(ctx, r.data).AdminUser.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, ErrAdminUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &port.AdminAccount{
		ID: row.ID, Username: row.Username, Nickname: row.Nickname, Avatar: row.Avatar,
		RoleID: row.RoleID, Enabled: row.Enabled, TOTPEnabled: len(row.TotpSecret) > 0,
	}, nil
}

// List 全部员工。
func (r *AdminUserRepoImpl) List(ctx context.Context) ([]port.AdminAccount, error) {
	rows, err := data.Client(ctx, r.data).AdminUser.Query().Order(ent.Asc(adminuser.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.AdminAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, port.AdminAccount{
			ID: row.ID, Username: row.Username, Nickname: row.Nickname, Avatar: row.Avatar,
			RoleID: row.RoleID, Enabled: row.Enabled, TOTPEnabled: len(row.TotpSecret) > 0,
		})
	}
	return out, nil
}

// Create 创建员工（用户名唯一；密码 bcrypt）。
func (r *AdminUserRepoImpl) Create(ctx context.Context, in port.AdminInput) (*port.AdminAccount, error) {
	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	row, err := data.Client(ctx, r.data).AdminUser.Create().
		SetUsername(in.Username).
		SetPasswordHash(hash).
		SetNickname(in.Nickname).
		SetAvatar(in.Avatar).
		SetRoleID(in.RoleID).
		SetRemark(in.Remark).
		SetEnabled(true).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return &port.AdminAccount{ID: row.ID, Username: row.Username, Nickname: row.Nickname, RoleID: row.RoleID, Enabled: true}, nil
}

// Update 更新员工（nil 字段不动；Enabled 非 nil 时切换）。
func (r *AdminUserRepoImpl) Update(ctx context.Context, id uint64, in port.AdminInput) (*port.AdminAccount, error) {
	q := data.Client(ctx, r.data).AdminUser.UpdateOneID(id)
	if in.Nickname != "" {
		q.SetNickname(in.Nickname)
	}
	if in.Avatar != "" {
		q.SetAvatar(in.Avatar)
	}
	if in.RoleID != 0 {
		q.SetRoleID(in.RoleID)
	}
	if in.Remark != "" {
		q.SetRemark(in.Remark)
	}
	if in.Enabled != nil {
		q.SetEnabled(*in.Enabled)
	}
	row, err := q.Save(ctx)
	if err != nil {
		return nil, err
	}
	return &port.AdminAccount{ID: row.ID, Username: row.Username, Nickname: row.Nickname, RoleID: row.RoleID, Enabled: row.Enabled, TOTPEnabled: len(row.TotpSecret) > 0}, nil
}

// RoleInUse 角色是否仍有员工挂载。
func (r *AdminUserRepoImpl) RoleInUse(ctx context.Context, roleID uint64) (bool, error) {
	return data.Client(ctx, r.data).AdminUser.Query().Where(adminuser.RoleID(roleID)).Exist(ctx)
}

// ResetPassword 重置员工密码（bcrypt）——凭据接管类操作，调用方须同时吊销会话。
func (r *AdminUserRepoImpl) ResetPassword(ctx context.Context, id uint64, password string) error {
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = data.Client(ctx, r.data).AdminUser.UpdateOneID(id).SetPasswordHash(hash).Save(ctx)
	return err
}

// ClearTOTP 解绑员工 TOTP（port.AdminMutator；幂等——未绑定时同样成功）。
func (r *AdminUserRepoImpl) ClearTOTP(ctx context.Context, id uint64) error {
	return r.ClearTOTPSecret(ctx, id)
}

// RevokeAdminSessions 吊销员工全部未吊销的管理面会话（密码重置/解绑 TOTP 后强制重登；
// user realm 会话不受影响）。
func (r *AdminUserRepoImpl) RevokeAdminSessions(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).Session.Update().
		Where(
			session.UserID(id),
			session.RealmEQ(session.RealmAdmin),
			session.RevokedAtIsNil(),
		).
		SetRevokedAt(time.Now().UTC()).
		Save(ctx)
	return err
}

// Delete 删除员工（同事务清其管理面会话，防残留可刷新的 refresh token）。
func (r *AdminUserRepoImpl) Delete(ctx context.Context, id uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		client := data.Client(ctx, r.data)
		if _, err := client.Session.Delete().
			Where(session.UserID(id), session.RealmEQ(session.RealmAdmin)).
			Exec(ctx); err != nil {
			return err
		}
		return client.AdminUser.DeleteOneID(id).Exec(ctx)
	})
}

// ── TOTP 仓储方法（）────────────────────────────────────

// SetTOTPSecret 存储 TOTP 密钥密文。
func (r *AdminUserRepoImpl) SetTOTPSecret(ctx context.Context, id uint64, secret []byte) error {
	_, err := data.Client(ctx, r.data).AdminUser.UpdateOneID(id).SetTotpSecret(secret).Save(ctx)
	return err
}

// ClearTOTPSecret 清除 TOTP 绑定。
func (r *AdminUserRepoImpl) ClearTOTPSecret(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).AdminUser.UpdateOneID(id).ClearTotpSecret().Save(ctx)
	return err
}
