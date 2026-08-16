package identity

// identity 身份认证模块（M0 收尾）：管理员 TOTP/登录失败锁定/异地告警/refresh 轮换。
// user realm 注册登录 M1a 交付。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
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

// lockEntry 内存锁定状态（M0 内存版；M1 评估 Redis/DB）。
type lockEntry struct {
	fails    int
	lockedAt time.Time
}

var (
	lockMu    sync.Mutex
	lockTable = map[uint64]*lockEntry{} // admin_user.id → 状态
)

// isLocked 检查并清理过期锁定。
func isLocked(id uint64) bool {
	lockMu.Lock()
	defer lockMu.Unlock()
	e, ok := lockTable[id]
	if !ok {
		return false
	}
	if time.Since(e.lockedAt) > lockDuration {
		delete(lockTable, id)
		return false
	}
	return e.fails >= lockThreshold
}

func recordFail(id uint64) {
	lockMu.Lock()
	defer lockMu.Unlock()
	e, ok := lockTable[id]
	if !ok {
		e = &lockEntry{}
		lockTable[id] = e
	}
	e.fails++
	e.lockedAt = time.Now()
}

func clearFails(id uint64) {
	lockMu.Lock()
	defer lockMu.Unlock()
	delete(lockTable, id)
}

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
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Admin        AdminUser
}

// AdminLogin 管理员登录：锁定检查 → bcrypt 校验 → TOTP 校验 → 签发双令牌。
func (uc *IdentityUsecase) AdminLogin(ctx context.Context, username, password, totpCode, clientIP string) (*AdminLoginResult, error) {
	u, err := uc.repo.FindByUsername(ctx, username)
	if err != nil {
		return nil, ErrLoginFailed
	}
	if !u.Enabled {
		return nil, ErrAdminDisabled
	}
	if isLocked(u.ID) {
		return nil, ErrLocked
	}
	if !crypto.VerifyPassword(u.PasswordHash, password) {
		recordFail(u.ID)
		return nil, ErrLoginFailed
	}
	// TOTP：已绑定则必须提供
	if len(u.TOTPSecretEnc) > 0 {
		if totpCode == "" {
			return nil, ErrTOTPRequired
		}
		secret, err := uc.cipher.Open(u.TOTPSecretEnc, []byte(fmt.Sprintf("totp:%d", u.ID)))
		if err != nil {
			return nil, ErrTOTPInvalid // 解密失败降级
		}
		if !authn.VerifyTOTP(string(secret), totpCode) {
			recordFail(u.ID)
			return nil, ErrTOTPInvalid
		}
	}
	clearFails(u.ID)
	_ = uc.repo.TouchLogin(ctx, u.ID, clientIP, time.Now().UTC())

	access, expiresAt, err := uc.signer.Issue(authn.RealmAdmin, u.ID, u.Username, u.RoleID)
	if err != nil {
		return nil, err
	}
	// refresh token（随机 32B，DB 存 SHA-256）
	refresh, err := uc.createSession(ctx, u.ID, clientIP)
	if err != nil {
		return nil, err
	}
	return &AdminLoginResult{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		Admin:        *u,
	}, nil
}

// RefreshAccess 用 refresh token 换新令牌对（一次性：旧 session 吊销 + 新 session）。
func (uc *IdentityUsecase) RefreshAccess(ctx context.Context, refreshToken string) (*AdminLoginResult, error) {
	hash := hashToken(refreshToken)
	sess, err := uc.data.Client.Session.Query().
		Where(
			session.RefreshTokenHash(hash),
			session.RealmEQ(session.RealmAdmin),
		).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	if !sess.RevokedAt.IsZero() || time.Now().After(sess.ExpiresAt) {
		return nil, ErrSessionInvalid
	}
	// 一次性：吊销旧
	_ = uc.data.Client.Session.UpdateOne(sess).SetRevokedAt(time.Now().UTC()).Exec(ctx)

	// 取用户重签
	row, err := uc.data.Client.AdminUser.Get(ctx, sess.UserID)
	if ent.IsNotFound(err) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	if !row.Enabled {
		return nil, ErrAdminDisabled
	}
	access, expiresAt, err := uc.signer.Issue(authn.RealmAdmin, row.ID, row.Username, row.RoleID)
	if err != nil {
		return nil, err
	}
	refresh, err := uc.createSession(ctx, row.ID, sess.IP)
	if err != nil {
		return nil, err
	}
	return &AdminLoginResult{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		Admin: AdminUser{ID: row.ID, Username: row.Username, Nickname: row.Nickname,
			Avatar: row.Avatar, RoleID: row.RoleID, Enabled: row.Enabled},
	}, nil
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

func (uc *IdentityUsecase) createSession(ctx context.Context, userID uint64, ip string) (string, error) {
	refresh := randomToken()
	_, err := uc.data.Client.Session.Create().
		SetRealm(session.RealmAdmin).
		SetUserID(userID).
		SetRefreshTokenHash(hashToken(refresh)).
		SetIP(ip).
		SetExpiresAt(time.Now().Add(14 * 24 * time.Hour).UTC()).
		Save(ctx)
	if err != nil {
		return "", err
	}
	return refresh, nil
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

// ── TOTP 管理 ─────────────────────────────────────────────────

// EnableTOTP 生成 TOTP 密钥（未确认状态——需管理员用 authenticator 扫码后调 ConfirmTOTP）。
func (uc *IdentityUsecase) EnableTOTP(ctx context.Context, adminID uint64, username string) (secret, otpauthURL string, err error) {
	secret, url, err := authn.GenerateTOTP(username)
	if err != nil {
		return "", "", err
	}
	enc, err := uc.cipher.Seal([]byte(secret), []byte(fmt.Sprintf("totp:%d", adminID)))
	if err != nil {
		return "", "", err
	}
	if err := uc.repo.SetTOTPSecret(ctx, adminID, enc); err != nil {
		return "", "", err
	}
	return secret, url, nil
}

// ConfirmTOTP 验证一次确认绑定。
func (uc *IdentityUsecase) ConfirmTOTP(ctx context.Context, adminID uint64, code string) error {
	u, err := uc.findAdminByID(ctx, adminID)
	if err != nil {
		return err
	}
	if len(u.TOTPSecretEnc) == 0 {
		return errors.New("identity.TOTP_NOT_ENABLED")
	}
	secret, err := uc.cipher.Open(u.TOTPSecretEnc, []byte(fmt.Sprintf("totp:%d", adminID)))
	if err != nil {
		return ErrTOTPInvalid
	}
	if !authn.VerifyTOTP(string(secret), code) {
		return ErrTOTPInvalid
	}
	return nil
}

// DisableTOTP 解绑（管理员自助或超管重置）。
func (uc *IdentityUsecase) DisableTOTP(ctx context.Context, adminID uint64) error {
	return uc.repo.ClearTOTPSecret(ctx, adminID)
}

func (uc *IdentityUsecase) findAdminByID(ctx context.Context, id uint64) (*AdminUser, error) {
	row, err := uc.data.Client.AdminUser.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, errors.New("identity.ADMIN_NOT_FOUND")
	}
	if err != nil {
		return nil, err
	}
	return &AdminUser{
		ID: row.ID, Username: row.Username, PasswordHash: row.PasswordHash,
		TOTPSecretEnc: row.TotpSecret, Enabled: row.Enabled,
	}, nil
}

// ── data.go 补充方法（仓储实现在此文件尾部声明接口匹配）────────────

// data cipher 包装（bootstrap 注入的 DataCipher 已有 Box()，此处直接用 crypto.Box 接口）
