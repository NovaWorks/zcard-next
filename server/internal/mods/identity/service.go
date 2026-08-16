package identity

// Kratos transport 薄层：登录/登出/刷新/TOTP 管理。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AdminAuthService 管理认证服务。
type AdminAuthService struct {
	adminv1.UnimplementedAdminAuthServiceServer
	uc *IdentityUsecase
}

// NewAdminAuthService 构造。
func NewAdminAuthService(uc *IdentityUsecase) *AdminAuthService {
	return &AdminAuthService{uc: uc}
}

// Login 管理员登录。
func (s *AdminAuthService) Login(ctx context.Context, req *adminv1.LoginRequest) (*adminv1.LoginReply, error) {
	res, err := s.uc.AdminLogin(ctx, req.GetUsername(), req.GetPassword(), req.GetTotpCode(), clientIP(ctx))
	if err != nil {
		return nil, mapLoginErr(err)
	}
	return &adminv1.LoginReply{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    res.ExpiresAt.Unix(),
		Admin:        toAdminProfile(res.Admin),
	}, nil
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
	res, err := s.uc.RefreshAccess(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, mapLoginErr(err)
	}
	return &adminv1.LoginReply{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    res.ExpiresAt.Unix(),
		Admin:        toAdminProfile(res.Admin),
	}, nil
}

// EnableTOTP 生成绑定密钥。
func (s *AdminAuthService) EnableTOTP(ctx context.Context, _ *emptypb.Empty) (*adminv1.EnableTOTPReply, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	secret, url, err := s.uc.EnableTOTP(ctx, claims.Subject, claims.Username)
	if err != nil {
		return nil, errors.InternalServer("identity.TOTP_ENABLE_FAILED", "生成密钥失败")
	}
	return &adminv1.EnableTOTPReply{Secret: secret, OtpauthUrl: url}, nil
}

// ConfirmTOTP 确认绑定。
func (s *AdminAuthService) ConfirmTOTP(ctx context.Context, req *adminv1.ConfirmTOTPRequest) (*emptypb.Empty, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if err := s.uc.ConfirmTOTP(ctx, claims.Subject, req.GetCode()); err != nil {
		return nil, errors.BadRequest("identity.TOTP_INVALID", "动态码错误")
	}
	return &emptypb.Empty{}, nil
}

// DisableTOTP 解绑。
func (s *AdminAuthService) DisableTOTP(ctx context.Context, req *adminv1.ConfirmTOTPRequest) (*emptypb.Empty, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if err := s.uc.ConfirmTOTP(ctx, claims.Subject, req.GetCode()); err != nil {
		return nil, errors.BadRequest("identity.TOTP_INVALID", "动态码错误")
	}
	if err := s.uc.DisableTOTP(ctx, claims.Subject); err != nil {
		return nil, errors.InternalServer("identity.TOTP_DISABLE_FAILED", "解绑失败")
	}
	return &emptypb.Empty{}, nil
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
	return &adminv1.GetProfileReply{Admin: toAdminProfile(*u)}, nil
}

func mapLoginErr(err error) error {
	switch {
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
	if tr, ok := transport.FromServerContext(ctx); ok {
		if hc, ok := tr.(khttp.Context); ok {
			return hc.Request().RemoteAddr
		}
	}
	return ""
}
