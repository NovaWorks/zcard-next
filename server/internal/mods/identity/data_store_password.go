package identity

// 用户自服务（）：找回密码（邮箱验证码）/改密/改资料。
// 安全模型（ + ）：
// - 防枚举：邮箱不存在同样返回成功（仅真实邮箱收码）
// - 防爆破：code_hash SHA-256 存储（库内无明文）；单条记录 attempt≥5 作废
// - 防滥发：同邮箱 60s 冷却；验证码 15 分钟；一次性（verified_at）
// - 改密踢线：sessions 批量吊销（realm=user）；响应携带新 token 保当前会话

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/emailverification"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// 找回密码参数。
const (
	resetCodeTTL      = 15 * time.Minute // 验证码有效期
	resetCodeCooldown = 60 * time.Second // 同邮箱重发冷却
	resetCodeMaxTry   = 5                // 单条验证码错误上限（作废需重发）
)

// PasswordService 找回/改密/改资料（挂在 StoreUserService，构造注入 Sender）。
type PasswordService struct {
	data   *data.Data
	signer *authn.Signer
	sender notifyport.Sender
}

// NewPasswordService 构造（wire）。
func NewPasswordService(d *data.Data, signer *authn.Signer, sender notifyport.Sender) *PasswordService {
	return &PasswordService{data: d, signer: signer, sender: sender}
}

func codeHash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// ForgotPassword 发码（防枚举：任何输入都成功；仅真实邮箱投递）。
func (s *PasswordService) ForgotPassword(ctx context.Context, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("identity.EMAIL_INVALID")
	}
	u, err := data.Client(ctx, s.data).User.Query().
		Where(user.Email(email)).Only(ctx)
	if err != nil {
		return nil // 防枚举：不存在同样成功
	}
	return s.sendResetCode(ctx, u)
}

// sendResetCode 生成验证码 + 冷却检查 + 邮件投递。
func (s *PasswordService) sendResetCode(ctx context.Context, u *ent.User) error {
	client := data.Client(ctx, s.data)
	// 60s 冷却：最新一条未过期未验证记录
	latest, err := client.EmailVerification.Query().
		Where(
			emailverification.Email(u.Email),
			emailverification.PurposeEQ(emailverification.PurposeReset),
			emailverification.VerifiedAtIsNil(),
		).
		Order(ent.Desc(emailverification.FieldCreatedAt)).
		First(ctx)
	if err == nil && time.Since(latest.CreatedAt) < resetCodeCooldown {
		return errors.New("identity.CODE_COOLDOWN") // 命中真实邮箱才暴露冷却（防枚举允许）
	}
	// 通道就绪前置校验（fail-fast;防枚举允许冷却差异,通道缺失必须明示）
	if cr, ok := s.sender.(interface {
		ChannelReady(ctx context.Context, channel string) bool
	}); ok && !cr.ChannelReady(ctx, "email") {
		return errors.New("identity.EMAIL_NOT_READY: 邮件通道未配置——请联系管理员在后台「设置 → 邮件短信」配置 SMTP 发件账号")
	}
	code, err := randomCode()
	if err != nil {
		return err
	}
	if _, err := client.EmailVerification.Create().
		SetEmail(u.Email).
		SetUserID(u.ID).
		SetPurpose(emailverification.PurposeReset).
		SetCodeHash(codeHash(code)).
		SetExpiresAt(time.Now().Add(resetCodeTTL)).
		Save(ctx); err != nil {
		return err
	}
	// 直发（不走模板：无模板即 skipped 的降级语义不适用于验证码场景）
	return s.sender.Send(ctx, notifyport.Message{
		EventType: "user.password_reset",
		Channel:   "email",
		Recipient: u.Email,
		Locale:    "zh_CN",
		Subject:   "ZCard 找回密码验证码",
		Body: fmt.Sprintf(
			"<p>您正在重置账户 <b>%s</b> 的密码。</p><p>验证码：<b style=\"font-size:20px\">%s</b>（%d 分钟内有效）。</p><p>若非本人操作请忽略本邮件。</p>",
			u.Username, code, int(resetCodeTTL.Minutes())),
		Variables: map[string]string{"username": u.Username},
		BizType:   "password_reset", BizID: u.ID,
	})
}

// ResetPassword 验码重置（一次性；吊销全部 session；重置即登录）。
func (s *PasswordService) ResetPassword(ctx context.Context, email, code, newPassword string) (*ent.User, error) {
	if len(newPassword) < 6 {
		return nil, errors.New("identity.PASSWORD_TOO_SHORT: 至少 6 位")
	}
	email = strings.TrimSpace(strings.ToLower(email))
	client := data.Client(ctx, s.data)
	v, err := client.EmailVerification.Query().
		Where(
			emailverification.Email(email),
			emailverification.PurposeEQ(emailverification.PurposeReset),
		).
		Order(ent.Desc(emailverification.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		return nil, errors.New("identity.CODE_INVALID") // 无记录（防枚举：统一错误）
	}
	// 校验链：过期/已用/超限/不匹配 全部统一 CODE_INVALID（不泄露差异）
	bad := !v.VerifiedAt.IsZero() ||
		time.Now().After(v.ExpiresAt) ||
		v.AttemptCount >= resetCodeMaxTry ||
		codeHash(code) != v.CodeHash
	if bad {
		_, _ = client.EmailVerification.UpdateOne(v).
			SetAttemptCount(v.AttemptCount + 1).Save(ctx)
		return nil, errors.New("identity.CODE_INVALID")
	}
	u, err := client.User.Get(ctx, v.UserID)
	if err != nil {
		return nil, errors.New("identity.CODE_INVALID")
	}
	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	err = data.Tx(ctx, s.data, func(txCtx context.Context) error {
		tc := data.Client(txCtx, s.data)
		if _, err := tc.User.UpdateOne(u).SetPasswordHash(hash).Save(txCtx); err != nil {
			return err
		}
		if _, err := tc.EmailVerification.UpdateOne(v).SetVerifiedAt(time.Now()).Save(txCtx); err != nil {
			return err
		}
		return revokeUserSessions(txCtx, tc, u.ID)
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ChangePassword 登录态改密（旧密码校验；吊销 session；新 token 保当前会话）。
func (s *PasswordService) ChangePassword(ctx context.Context, userID uint64, oldPassword, newPassword string) (*ent.User, error) {
	if len(newPassword) < 6 {
		return nil, errors.New("identity.PASSWORD_TOO_SHORT: 至少 6 位")
	}
	client := data.Client(ctx, s.data)
	u, err := client.User.Get(ctx, userID)
	if err != nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	if !crypto.VerifyPassword(u.PasswordHash, oldPassword) {
		return nil, errors.New("identity.BAD_CREDENTIALS")
	}
	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	err = data.Tx(ctx, s.data, func(txCtx context.Context) error {
		tc := data.Client(txCtx, s.data)
		if _, err := tc.User.UpdateOne(u).SetPasswordHash(hash).Save(txCtx); err != nil {
			return err
		}
		return revokeUserSessions(txCtx, tc, u.ID)
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateProfile 改邮箱（唯一性校验；空=不改）。
func (s *PasswordService) UpdateProfile(ctx context.Context, userID uint64, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("identity.EMAIL_INVALID")
	}
	client := data.Client(ctx, s.data)
	_, err := client.User.UpdateOneID(userID).SetEmail(email).Save(ctx)
	if err != nil {
		return errors.New("identity.EMAIL_TAKEN") // 唯一索引冲突
	}
	return nil
}

// revokeUserSessions 吊销用户全部 user session（改密/重置踢线）。
func revokeUserSessions(ctx context.Context, client *ent.Client, userID uint64) error {
	_, err := client.Session.Update().
		Where(
			session.RealmEQ(session.RealmUser),
			session.UserID(userID),
			session.RevokedAtIsNil(),
		).
		SetRevokedAt(time.Now()).
		Save(ctx)
	return err
}
