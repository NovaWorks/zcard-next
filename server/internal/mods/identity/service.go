package identity

// Kratos transport 薄层：登录/登出/刷新/TOTP 管理。

import (
	"context"
	"net"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	authzport "github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/captcha"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AdminAuthService 管理认证服务。
type AdminAuthService struct {
	adminv1.UnimplementedAdminAuthServiceServer
	uc  *IdentityUsecase
	az  authzport.Authorizer
	cap *captcha.Service // 后台登录图形验证码（captcha_admin_login 场景）
}

// NewAdminAuthService 构造（az 用于 profile 下发权限点清单，前端动态路由消费）。
func NewAdminAuthService(uc *IdentityUsecase, az authzport.Authorizer, cap *captcha.Service) *AdminAuthService {
	return &AdminAuthService{uc: uc, az: az, cap: cap}
}

// Login 管理员登录（captcha_admin_login 开启时前置图形验证码；未开启放行）。
func (s *AdminAuthService) Login(ctx context.Context, req *adminv1.LoginRequest) (*adminv1.LoginReply, error) {
	if err := s.uc.CheckLoginIP(ctx, clientIP(ctx)); err != nil {
		return nil, mapLoginErr(err)
	}
	if s.cap != nil && req.GetChallenge() == "" {
		if err := s.cap.VerifyScene(ctx, captcha.SceneAdminLogin, req.GetCaptchaId(), req.GetCaptchaCode()); err != nil {
			return nil, mapLoginErr(err)
		}
	}
	noCache(ctx)
	var res *AdminLoginResult
	var err error
	if req.GetChallenge() != "" {
		res, err = s.uc.FinishLogin(ctx, req.GetChallenge(), req.GetTotpCode(), clientIP(ctx))
	} else {
		res, err = s.uc.StartLogin(ctx, req.GetUsername(), req.GetPassword(), clientIP(ctx))
	}
	if err != nil {
		return nil, mapLoginErr(err)
	}
	return &adminv1.LoginReply{
		RequiresTotp: res.Challenge != "", Challenge: res.Challenge, RecoveryTicket: res.RecoveryTicket,
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    tokenExpiry(res.ExpiresAt),
		Admin:        toAdminProfile(res.Admin),
	}, nil
}

// GetCaptchaImage 登录图形验证码（免鉴权）。
func (s *AdminAuthService) GetCaptchaImage(ctx context.Context, _ *emptypb.Empty) (*adminv1.CaptchaImageReply, error) {
	id, b64, err := s.cap.Get()
	if err != nil {
		return nil, errors.InternalServer("identity.CAPTCHA_GEN_FAILED", "生成验证码失败")
	}
	return &adminv1.CaptchaImageReply{CaptchaId: id, ImageBase64: b64}, nil
}

// GetCaptchaConfig 登录验证码开关（免鉴权；登录页条件渲染）。
func (s *AdminAuthService) GetCaptchaConfig(ctx context.Context, _ *emptypb.Empty) (*adminv1.CaptchaConfigReply, error) {
	return &adminv1.CaptchaConfigReply{Enabled: s.cap.SceneEnabledFor(ctx, captcha.SceneAdminLogin)}, nil
}

// Logout 登出（吊销 refresh session）。
func (s *AdminAuthService) Logout(ctx context.Context, req *adminv1.LogoutRequest) (*emptypb.Empty, error) {
	if req.GetRefreshToken() != "" {
		_ = s.uc.Logout(ctx, req.GetRefreshToken())
	}
	return &emptypb.Empty{}, nil
}

// RefreshToken 用 refresh 换新令牌对。
func (s *AdminAuthService) RefreshToken(ctx context.Context, req *adminv1.RefreshTokenRequest) (*adminv1.LoginReply, error) {
	noCache(ctx)
	res, err := s.uc.RefreshAccess(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, mapLoginErr(err)
	}
	return &adminv1.LoginReply{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    tokenExpiry(res.ExpiresAt),
		Admin:        toAdminProfile(res.Admin),
	}, nil
}

func noCache(ctx context.Context) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		tr.ReplyHeader().Set("Cache-Control", "no-store")
		tr.ReplyHeader().Set("Pragma", "no-cache")
	}
}
func tokenExpiry(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
func (s *AdminAuthService) EnableTOTP(ctx context.Context, req *adminv1.ConfirmTOTPRequest) (*adminv1.EnableTOTPReply, error) {
	noCache(ctx)
	c := ClaimsFromContext(ctx)
	if c == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	secret, url, err := s.uc.SetupMFA(ctx, c.Subject, c.AuthVersion, req.GetPassword(), req.GetCode(), req.GetRecoveryTicket())
	if err != nil {
		return nil, mapMFAErr(err)
	}
	return &adminv1.EnableTOTPReply{Secret: secret, OtpauthUrl: url}, nil
}
func (s *AdminAuthService) ConfirmTOTP(ctx context.Context, req *adminv1.ConfirmTOTPRequest) (*adminv1.RecoveryCodesReply, error) {
	noCache(ctx)
	c := ClaimsFromContext(ctx)
	if c == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	codes, err := s.uc.ConfirmMFA(ctx, c.Subject, c.AuthVersion, req.GetCode())
	if err != nil {
		return nil, mapMFAErr(err)
	}
	return &adminv1.RecoveryCodesReply{RecoveryCodes: codes}, nil
}
func (s *AdminAuthService) DisableTOTP(ctx context.Context, req *adminv1.ConfirmTOTPRequest) (*emptypb.Empty, error) {
	noCache(ctx)
	c := ClaimsFromContext(ctx)
	if c == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if err := s.uc.DisableMFA(ctx, c.Subject, c.AuthVersion, req.GetPassword(), req.GetCode()); err != nil {
		return nil, mapMFAErr(err)
	}
	return &emptypb.Empty{}, nil
}
func (s *AdminAuthService) ResetTOTP(ctx context.Context, req *adminv1.ResetTOTPRequest) (*emptypb.Empty, error) {
	noCache(ctx)
	c := ClaimsFromContext(ctx)
	if c == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if !s.az.Allowed(ctx, c.RoleID, "identity:admin_totp_reset") {
		return nil, errors.Forbidden("authz.PERMISSION_DENIED", "权限不足")
	}
	if err := s.uc.ResetOtherMFA(ctx, c.Subject, req.GetAdminId(), c.AuthVersion, req.GetPassword(), req.GetCode(), req.GetReason()); err != nil {
		return nil, mapMFAErr(err)
	}
	return &emptypb.Empty{}, nil
}
func mapMFAErr(err error) error {
	switch {
	case errors.Is(err, ErrIPLimited):
		return errors.New(429, "identity.IP_LIMITED", "请求过于频繁，请稍后重试")
	case errors.Is(err, ErrMFAExpired):
		return errors.BadRequest("identity.MFA_EXPIRED", "验证已过期，请重新开始")
	case errors.Is(err, ErrLocked):
		return errors.BadRequest("identity.ACCOUNT_LOCKED", "失败次数过多，请 15 分钟后重试")
	case errors.Is(err, ErrLoginFailed):
		return errors.BadRequest("identity.LOGIN_FAILED", "密码错误")
	case errors.Is(err, ErrTOTPInvalid), errors.Is(err, ErrTOTPRequired):
		return errors.BadRequest("identity.TOTP_INVALID", "动态码或恢复码无效；已使用的动态码请等待下一组")
	case errors.Is(err, ErrSessionInvalid):
		return errors.Unauthorized("identity.SESSION_INVALID", "安全设置已变更，请重新登录")
	case errors.Is(err, ErrMFAConflict):
		return errors.BadRequest("identity.MFA_CONFLICT", "状态已变化或操作参数无效，请刷新后重试")
	default:
		return errors.InternalServer("identity.MFA_FAILED", "操作失败，请重试或联系管理员")
	}
}

// GetProfile 当前管理员信息。
func (s *AdminAuthService) GetProfile(ctx context.Context, _ *emptypb.Empty) (*adminv1.GetProfileReply, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	u, err := s.uc.repo.FindByUsername(ctx, claims.Username)
	if err != nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	// 权限点清单下发（auth.proto GetProfileReply.permissions——前端据此生成菜单与按钮）。
	// 读取失败时下发空清单（fail-closed：后端中间件每请求仍独立校验，不受此快照影响）。
	perms, _ := s.az.PermissionsOf(ctx, claims.RoleID)
	if perms == nil {
		perms = []string{}
	}
	return &adminv1.GetProfileReply{Admin: toAdminProfile(*u), Permissions: perms}, nil
}

func mapLoginErr(err error) error {
	switch {
	case errors.Is(err, ErrIPLimited):
		return errors.New(429, "identity.IP_LIMITED", "请求过于频繁，请稍后重试")
	case errors.Is(err, ErrMFAExpired), errors.Is(err, ErrTOTPInvalid), errors.Is(err, ErrMFAConflict):
		return mapMFAErr(err)
	case errors.Is(err, captcha.ErrCaptchaRequired):
		return errors.BadRequest("identity.CAPTCHA_REQUIRED", "请输入图形验证码")
	case errors.Is(err, captcha.ErrCaptchaInvalid):
		return errors.BadRequest("identity.CAPTCHA_INVALID", "图形验证码错误或已过期")
	case errors.Is(err, ErrAdminDisabled):
		return errors.Forbidden("identity.ADMIN_DISABLED", "账号已禁用")
	case errors.Is(err, ErrLocked):
		return errors.Forbidden("identity.ACCOUNT_LOCKED", "登录失败次数过多，账号已临时锁定")
	case errors.Is(err, ErrTOTPRequired):
		return errors.Unauthorized("identity.TOTP_REQUIRED", "需要两步验证码")
	case errors.Is(err, ErrTOTPInvalid):
		return errors.Unauthorized("identity.TOTP_INVALID", "两步验证码错误")
	case errors.Is(err, ErrSessionInvalid):
		return errors.Unauthorized("identity.SESSION_INVALID", "会话无效或已过期")
	default:
		return errors.Unauthorized("identity.LOGIN_FAILED", "账号或密码错误")
	}
}

func toAdminProfile(u AdminUser) *adminv1.AdminProfile {
	p := &adminv1.AdminProfile{
		TotpBoundAt: u.TOTPBoundAt,
		Id:          u.ID,
		Username:    u.Username,
		Nickname:    u.Nickname,
		Avatar:      u.Avatar,
		RoleId:      u.RoleID,
		TotpEnabled: len(u.TOTPSecretEnc) > 0,
		LastLoginIp: u.LastLoginIP,
	}
	if !u.LastLoginAt.IsZero() {
		p.LastLoginAt = timestamppb.New(u.LastLoginAt)
	}
	return p
}

func clientIP(ctx context.Context) string {
	if r, ok := khttp.RequestFromServerContext(ctx); ok {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			return host
		}
		return r.RemoteAddr
	}
	return ""

}

func (s *AdminAuthService) CancelTOTP(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	c := ClaimsFromContext(ctx)
	if c == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if err := s.uc.CancelMFA(ctx, c.Subject, c.AuthVersion); err != nil {
		return nil, mapMFAErr(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminAuthService) ChangePassword(ctx context.Context, req *adminv1.ChangeAdminPasswordRequest) (*emptypb.Empty, error) {
	noCache(ctx)
	c := ClaimsFromContext(ctx)
	if c == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "请先登录")
	}
	err := s.uc.ChangeOwnPassword(ctx, c.Subject, c.AuthVersion, req.GetCurrentPassword(), req.GetNewPassword(), req.GetConfirmPassword())
	switch {
	case err == nil:
		return &emptypb.Empty{}, nil
	case errors.Is(err, ErrPasswordInput):
		return nil, errors.BadRequest("identity.PASSWORD_INPUT", "请检查新密码长度（至少 6 个字符）、两次输入是否一致，以及是否与当前密码相同")
	case errors.Is(err, ErrLoginFailed):
		return nil, errors.BadRequest("identity.CURRENT_PASSWORD_INVALID", "当前密码不正确")
	case errors.Is(err, ErrLocked):
		return nil, errors.New(429, "identity.ACCOUNT_LOCKED", "密码验证失败次数过多，请稍后重试")
	case errors.Is(err, ErrSessionInvalid), errors.Is(err, ErrAdminDisabled):
		return nil, mapLoginErr(err)
	case errors.Is(err, ErrMFAConflict):
		return nil, errors.Conflict("identity.SECURITY_CONFLICT", "账号安全状态已变化，请重新登录后再试")
	default:
		return nil, errors.InternalServer("identity.PASSWORD_CHANGE_FAILED", "修改密码失败，请稍后重试")
	}
}
