package identity

// identity 身份认证模块（M0 收尾）：管理员 TOTP/登录失败锁定/异地告警/refresh 轮换。
// user realm 注册登录 M1a 交付。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// 错误 reason（附录 B 区段：identity.*）
var (
	// ErrLoginFailed 登录失败（账号或密码错误——对外不区分，防枚举）。
	ErrLoginFailed = errors.New("identity.LOGIN_FAILED")
	// ErrAdminDisabled 账号已禁用。
	ErrAdminDisabled = errors.New("identity.ADMIN_DISABLED")
	// ErrLocked 登录失败次数过多，账号被锁定。
	ErrLocked = errors.New("identity.ACCOUNT_LOCKED")
	// ErrTOTPRequired 需要两步验证码。
	ErrTOTPRequired = errors.New("identity.TOTP_REQUIRED")
	// ErrTOTPInvalid 两步验证码错误。
	ErrTOTPInvalid = errors.New("identity.TOTP_INVALID")
	// ErrSessionInvalid refresh 令牌无效或已过期。
	ErrSessionInvalid = errors.New("identity.SESSION_INVALID")
)

// 登录锁定参数。
const (
	lockThreshold = 5                // 连续失败次数
	lockDuration  = 15 * time.Minute // 锁定时长
)

// AdminUserRepo 员工仓储（模块内端口）。
type AdminUserRepo interface {
	FindByUsername(ctx context.Context, username string) (*AdminUser, error)
	TouchLogin(ctx context.Context, id uint64, ip string, at time.Time) error
	// TOTP
	SetTOTPSecret(ctx context.Context, id uint64, secret []byte) error
	ClearTOTPSecret(ctx context.Context, id uint64) error
}

// AdminUser 员工聚合。
type AdminUser struct {
	TOTPBoundAt   int64
	ID            uint64
	Username      string
	PasswordHash  string
	Nickname      string
	Avatar        string
	RoleID        uint64
	TOTPSecretEnc []byte
	Enabled       bool
	LastLoginIP   string
	LastLoginAt   time.Time
}

// IdentityUsecase 身份用例。
type IdentityUsecase struct {
	repo   AdminUserRepo
	signer *authn.Signer
	data   *data.Data
	cipher *crypto.Box // ZCARD_DATA_KEY 解密 TOTP 密钥
}

// NewIdentityUsecase 构造。
func NewIdentityUsecase(repo AdminUserRepo, signer *authn.Signer, d *data.Data, box *crypto.Box) *IdentityUsecase {
	return &IdentityUsecase{repo: repo, signer: signer, data: d, cipher: box}
}

// AdminLoginResult 登录结果。
type AdminLoginResult struct {
	Challenge      string
	RecoveryTicket string
	AccessToken    string
	RefreshToken   string
	ExpiresAt      time.Time
	Admin          AdminUser
}

// AdminLogin 管理员登录：锁定检查 → bcrypt 校验 → TOTP 校验 → 签发双令牌。
func (uc *IdentityUsecase) AdminLogin(ctx context.Context, username, password, totpCode, clientIP string) (*AdminLoginResult, error) {
	return uc.passwordLogin(ctx, username, password, totpCode, clientIP, false)
}

// RefreshAccess 用 refresh token 换新令牌对（一次性：旧 session 吊销 + 新 session）。
func (uc *IdentityUsecase) RefreshAccess(ctx context.Context, refreshToken string) (*AdminLoginResult, error) {
	sess, err := uc.data.Client.Session.Query().Where(session.RefreshTokenHash(hashToken(refreshToken)), session.RealmEQ(session.RealmAdmin)).Only(ctx)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	var out *AdminLoginResult
	err = mfaChange(ctx, uc.data, sess.UserID, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if !u.Enabled || u.AuthVersion != sess.AuthVersion || time.Now().After(sess.ExpiresAt) {
			return ErrSessionInvalid
		}
		n, err := data.Client(ctx, uc.data).Session.Update().Where(session.ID(sess.ID), session.RevokedAtIsNil()).SetRevokedAt(time.Now().UTC()).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSessionInvalid
		}
		out, err = uc.issueSession(ctx, u, sess.IP, sess.ExpiresAt)
		return err
	})
	return out, err
}

// Logout 吊销指定 refresh token。
func (uc *IdentityUsecase) Logout(ctx context.Context, refreshToken string) error {
	hash := hashToken(refreshToken)
	_, err := uc.data.Client.Session.Update().
		Where(session.RefreshTokenHash(hash)).
		SetRevokedAt(time.Now().UTC()).
		Save(ctx)
	return err
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := randRead(b); err != nil {
		panic("identity: 随机数生成失败: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func randRead(b []byte) (int, error) { return rand.Read(b) }
