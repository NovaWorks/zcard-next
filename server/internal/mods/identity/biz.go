// Package identity 身份认证模块（M0）：管理员/员工/顾客的注册、登录、会话、TOTP。
//
// 双 realm JWT（admin/user 独立密钥，防提权串用）；登录失败锁定与异地告警
// 接 security_audit_logs（M1）；顾客 user realm 注册登录 M1a 交付。
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// 错误 reason（附录 B 区段：identity.*；message 走 i18n，前端按 reason 处理）。
var (
	// ErrLoginFailed 登录失败（账号或密码错误——对外不区分，防枚举）。
	ErrLoginFailed = errors.New("identity.LOGIN_FAILED")
	// ErrAdminDisabled 账号已禁用。
	ErrAdminDisabled = errors.New("identity.ADMIN_DISABLED")
)

// AdminUserRepo 员工仓储（模块内端口，实现于 data.go，wire 装配）。
type AdminUserRepo interface {
	FindByUsername(ctx context.Context, username string) (*AdminUser, error)
	TouchLogin(ctx context.Context, id uint64, ip string, at time.Time) error
}

// AdminUser 员工聚合（含敏感字段，绝不经 port 下发）。
type AdminUser struct {
	ID            uint64
	Username      string
	PasswordHash  string
	Nickname      string
	Avatar        string
	RoleID        uint64
	TOTPSecretEnc []byte // AES-GCM 密文
	Enabled       bool
	LastLoginIP   string
	LastLoginAt   time.Time
}

// IdentityUsecase 身份用例（M0：admin 登录；M1a：user realm 注册/登录/找回）。
type IdentityUsecase struct {
	repo   AdminUserRepo
	signer *authn.Signer
}

// NewIdentityUsecase 构造。
func NewIdentityUsecase(repo AdminUserRepo, signer *authn.Signer) *IdentityUsecase {
	return &IdentityUsecase{repo: repo, signer: signer}
}

// AdminLoginResult 登录结果。
type AdminLoginResult struct {
	AccessToken string
	ExpiresAt   time.Time
	Admin       AdminUser
}

// AdminLogin 管理员登录：bcrypt constant-time 校验 → 签发 admin realm JWT。
// 登录审计（成功/失败/IP）M1 接 security_audit_logs；TOTP 校验 M0 后续补齐。
func (uc *IdentityUsecase) AdminLogin(ctx context.Context, username, password, totpCode, clientIP string) (*AdminLoginResult, error) {
	u, err := uc.repo.FindByUsername(ctx, username)
	if err != nil {
		return nil, ErrLoginFailed
	}
	if !u.Enabled {
		return nil, ErrAdminDisabled
	}
	if !crypto.VerifyPassword(u.PasswordHash, password) {
		return nil, ErrLoginFailed
	}
	// TODO(M0): TOTP 校验（u.TOTPSecretEnc 解密后 pquerna/otp 验证）
	token, expiresAt, err := uc.signer.Issue(authn.RealmAdmin, u.ID, u.Username, u.RoleID)
	if err != nil {
		return nil, err
	}
	_ = uc.repo.TouchLogin(ctx, u.ID, clientIP, time.Now().UTC()) // 失败不影响登录
	return &AdminLoginResult{AccessToken: token, ExpiresAt: expiresAt, Admin: *u}, nil
}
