package identity

// Kratos transport 薄层（规划 §4.4：service 只做参数校验与装配，业务逻辑必须在 biz 层）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 权限点（§5.14 权限目录由路由表自动生成，M1；当前见 internal/server/middleware.go）：
//   auth:profile —— 当前身份（GetProfile）

// AdminAuthService 管理认证服务（实现 adminv1.AdminAuthService）。
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
	ip := clientIP(ctx)
	res, err := s.uc.AdminLogin(ctx, req.GetUsername(), req.GetPassword(), req.GetTotpCode(), ip)
	if err != nil {
		switch {
		case errors.Is(err, ErrAdminDisabled):
			return nil, errors.Forbidden("identity.ADMIN_DISABLED", "账号已禁用")
		default:
			// 登录失败统一 401，不区分「账号不存在/密码错误」（防枚举）
			return nil, errors.Unauthorized("identity.LOGIN_FAILED", "账号或密码错误")
		}
	}
	return &adminv1.LoginReply{
		AccessToken: res.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   res.ExpiresAt.Unix(),
		Admin:       toAdminProfile(res.Admin),
	}, nil
}

// Logout 登出（JWT 无状态，前端弃用令牌；M3 refresh 轮换后服务端吊销）。
func (*AdminAuthService) Logout(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

// GetProfile 当前管理员信息（JWT 鉴权后由 server 中间件注入 claims 上下文）。
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

func toAdminProfile(u AdminUser) *adminv1.AdminProfile {
	p := &adminv1.AdminProfile{
		Id:          u.ID,
		Username:    u.Username,
		Nickname:    u.Nickname,
		Avatar:      u.Avatar,
		RoleId:      u.RoleID,
		TotpEnabled: len(u.TOTPSecretEnc) > 0,
	}
	if !u.LastLoginAt.IsZero() {
		p.LastLoginAt = timestamppb.New(u.LastLoginAt)
	}
	p.LastLoginIp = u.LastLoginIP
	return p
}

// clientIP 从 transport 上下文取客户端 IP（登录审计用）。
func clientIP(ctx context.Context) string {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return ""
	}
	if hc, ok := tr.(http.Context); ok {
		return hc.Request().RemoteAddr
	}
	return ""
}
