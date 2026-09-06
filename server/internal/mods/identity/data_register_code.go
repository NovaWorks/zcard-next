package identity

// 注册验证码（security.register_method 驱动：username=免验证 / email=邮箱码 /
// phone=短信码）。复用 EmailVerification 表 + PasswordService 同款纪律：
// 6 位码哈希存储、10 分钟有效、60s 冷却、5 次尝试上限、一次性 verified。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/emailverification"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// 注册验证码参数（与找回密码同纪律）。
const (
	regCodeTTL      = 10 * time.Minute
	regCodeCooldown = 60 * time.Second
	regCodeMaxTry   = 5
)

// RegisterCodeService 注册验证码（挂在 StoreUserService；settings 读通道/模板）。
type RegisterCodeService struct {
	data     *data.Data
	sender   notifyport.Sender
	settings notifyport.SettingsReader // notify 组模板读取（nil=默认文案）
}

// NewRegisterCodeService 构造。
func NewRegisterCodeService(d *data.Data, sender notifyport.Sender) *RegisterCodeService {
	return &RegisterCodeService{data: d, sender: sender}
}

// SetSettings 装配期注入 settings（可选；nil 用内置默认文案）。
func (s *RegisterCodeService) SetSettings(r notifyport.SettingsReader) { s.settings = r }

// siteName 站点名（settings.site.name；缺失/读取失败回落 "ZCard"）。
func (s *RegisterCodeService) siteName(ctx context.Context) string {
	if s.settings != nil {
		if raw, err := s.settings.GetJSON(ctx, "site", "name"); err == nil && len(raw) > 0 {
			var v string
			if json.Unmarshal(raw, &v) == nil && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return "ZCard"
}

// renderSmsTemplate 短信模板渲染（{code}/{minutes}/{site} 占位；读取失败用 dflt）。
func (s *RegisterCodeService) renderSmsTemplate(ctx context.Context, key, dflt, code string) string {
	tpl := dflt
	if s.settings != nil {
		if raw, err := s.settings.GetJSON(ctx, "notify", key); err == nil && len(raw) > 0 {
			var v string
			if json.Unmarshal(raw, &v) == nil && strings.TrimSpace(v) != "" {
				tpl = v
			}
		}
	}
	r := strings.NewReplacer(
		"{code}", code,
		"{minutes}", fmt.Sprintf("%d", int(regCodeTTL.Minutes())),
		"{site}", s.siteName(ctx),
	)
	return r.Replace(tpl)
}

// smsTemplateParams 短信模板变量（与 renderSmsTemplate 占位同源；Body 发送给通道）。
// 各通道按自身模板变量规范转换（阿里云/七牛键值对象；腾讯云按变量名排序转数组）。
func (s *RegisterCodeService) smsTemplateParams(ctx context.Context, code string) map[string]string {
	return map[string]string{
		"code":    code,
		"minutes": fmt.Sprintf("%d", int(regCodeTTL.Minutes())),
		"site":    s.siteName(ctx),
	}
}

// SendRegisterCode 发码：target（邮箱或手机号）+ channel（email|phone，空自动判定）。
// 目标已被注册时拒绝（注册场景无需防枚举——注册地址本来就不存在）。
func (s *RegisterCodeService) SendRegisterCode(ctx context.Context, target, channel string) error {
	target = strings.TrimSpace(target)
	if channel == "" {
		channel = detectChannel(target)
	}
	if channel == "email" {
		target = strings.ToLower(target)
		if !strings.Contains(target, "@") || !strings.Contains(target, ".") {
			return errors.New("identity.EMAIL_INVALID: 邮箱格式不正确")
		}
	} else if channel == "phone" {
		if !isPhoneFormat(target) {
			return errors.New("identity.PHONE_INVALID: 手机号格式不正确")
		}
	} else {
		return errors.New("identity.CHANNEL_INVALID")
	}

	client := data.Client(ctx, s.data)
	// 目标已注册拒绝（含软删除域：username/email/phone 唯一索引兜底）
	exist, _ := client.User.Query().Where(
		user.Or(user.Email(target), user.Phone(target)),
	).Exist(ctx)
	if exist {
		return errors.New("identity.TARGET_TAKEN: 该邮箱/手机号已注册")
	}

	purpose := emailverification.PurposeRegister
	notifyChannel := "email"
	if channel == "phone" {
		purpose = emailverification.PurposePhoneRegister
		notifyChannel = "sms"
	}
	// 通道就绪前置校验（fail-fast）：验证码是「必须送达」场景——Send 对未配置通道
	// 按营销降级语义返回成功，用户却永远收不到码（实测踩坑），必须在发码前拦下。
	if cr, ok := s.sender.(interface {
		ChannelReady(ctx context.Context, channel string) bool
	}); ok && !cr.ChannelReady(ctx, notifyChannel) {
		if notifyChannel == "sms" {
			return errors.New("identity.SMS_NOT_READY: 短信通道未配置——请联系管理员在后台「设置 → 邮件短信」配置短信服务商")
		}
		return errors.New("identity.EMAIL_NOT_READY: 邮件通道未配置——请联系管理员在后台「设置 → 邮件短信」配置 SMTP 发件账号")
	}

	// 60s 冷却
	latest, err := client.EmailVerification.Query().
		Where(
			emailverification.Email(target),
			emailverification.PurposeEQ(purpose),
			emailverification.VerifiedAtIsNil(),
		).
		Order(ent.Desc(emailverification.FieldCreatedAt)).
		First(ctx)
	if err == nil && time.Since(latest.CreatedAt) < regCodeCooldown {
		return errors.New("identity.CODE_COOLDOWN: 发送过于频繁，请 1 分钟后再试")
	}

	code, err := randomCode()
	if err != nil {
		return err
	}
	if _, err := client.EmailVerification.Create().
		SetEmail(target).
		SetPurpose(purpose).
		SetCodeHash(codeHash(code)).
		SetExpiresAt(time.Now().Add(regCodeTTL)).
		Save(ctx); err != nil {
		return err
	}
	// 消息体：短信 Body = 模板变量 JSON（通道按自身规范转换；渲染文本入 Subject 留档），邮件用 HTML
	subject := "ZCard 注册验证码"
	var body string
	if channel == "phone" {
		params, _ := json.Marshal(s.smsTemplateParams(ctx, code))
		body = string(params)
		subject = s.renderSmsTemplate(ctx, "sms_template_register",
			"【ZCard】您的注册验证码：{code}，{minutes} 分钟内有效。", code)
	} else {
		body = fmt.Sprintf(
			"<p>您的注册验证码：<b style=\"font-size:20px\">%s</b>（%d 分钟内有效）。</p><p>若非本人操作请忽略。</p>",
			code, int(regCodeTTL.Minutes()))
	}
	return s.sender.Send(ctx, notifyport.Message{
		EventType: "user.register_code",
		Channel:   notifyChannel,
		Recipient: target,
		Locale:    "zh_CN",
		Subject:   subject,
		Body:      body,
		BizType:   "register_code",
	})
}

// VerifyRegisterCode 验码（一次性 verified；尝试计数；错满作废）。
// 返回 nil 表示通过。
func (s *RegisterCodeService) VerifyRegisterCode(ctx context.Context, target, code, channel string) error {
	target = strings.TrimSpace(target)
	if channel == "email" {
		target = strings.ToLower(target)
	}
	purpose := emailverification.PurposeRegister
	if channel == "phone" {
		purpose = emailverification.PurposePhoneRegister
	}
	client := data.Client(ctx, s.data)
	v, err := client.EmailVerification.Query().
		Where(
			emailverification.Email(target),
			emailverification.PurposeEQ(purpose),
		).
		Order(ent.Desc(emailverification.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		return errors.New("identity.CODE_INVALID: 请先获取验证码")
	}
	if !v.VerifiedAt.IsZero() {
		return errors.New("identity.CODE_USED: 验证码已使用")
	}
	if time.Now().After(v.ExpiresAt) {
		return errors.New("identity.CODE_EXPIRED: 验证码已过期")
	}
	if v.AttemptCount >= regCodeMaxTry {
		return errors.New("identity.CODE_MAX_TRIES: 错误次数过多，请重新获取")
	}
	if v.CodeHash != codeHash(strings.TrimSpace(code)) {
		_, _ = client.EmailVerification.UpdateOne(v).
			SetAttemptCount(v.AttemptCount + 1).Save(ctx)
		return errors.New("identity.CODE_INVALID: 验证码错误")
	}
	_, _ = client.EmailVerification.UpdateOne(v).
		SetVerifiedAt(time.Now()).Save(ctx)
	return nil
}

// detectChannel 按 target 格式自动判定通道。
func detectChannel(target string) string {
	if strings.Contains(target, "@") {
		return "email"
	}
	if isPhoneFormat(target) {
		return "phone"
	}
	return ""
}

// isPhoneFormat 手机号格式（1 开头 11 位——宽松口径，详细校验在短信通道侧）。
func isPhoneFormat(s string) bool {
	if len(s) != 11 || s[0] != '1' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
