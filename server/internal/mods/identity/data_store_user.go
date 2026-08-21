package identity

// StoreUserService 用户中心（P3-04 前置）：注册/登录/Me（user realm JWT）。
// 归因链：invite_code=<user_id> → l1=邀请人 l2=邀请人.l1 l3=邀请人.l2（环状拒绝）。

import (
	"context"
	"errors"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/captcha"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerprofile"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"

	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreUserService 用户中心服务。
type StoreUserService struct {
	storefrontv1.UnimplementedStoreUserServiceServer
	repo     *UserRepo
	signer   *authn.Signer
	data     *data.Data
	pwd      *PasswordService      // P3-10 自服务（找回/改密/改资料）
	regCode  *RegisterCodeService  // 注册验证码（email/phone 通道）
	regCfg   *RegisterCodeSettings // security 组注册开关/方式
	captcha  *captcha.Service      // 图形验证码（scene: login/register/reset）
}

// NewStoreUserService 构造。
func NewStoreUserService(repo *UserRepo, signer *authn.Signer, d *data.Data, pwd *PasswordService, regCode *RegisterCodeService, regCfg *RegisterCodeSettings, cap *captcha.Service) *StoreUserService {
	return &StoreUserService{repo: repo, signer: signer, data: d, pwd: pwd, regCode: regCode, regCfg: regCfg, captcha: cap}
}

// UserRepo 用户仓储。
type UserRepo struct{ data *data.Data }

// NewUserRepo 构造。
func NewUserRepo(d *data.Data) *UserRepo { return &UserRepo{data: d} }

// Username 按 ID 取用户名（port.UserReader 实现；audit 安全日志主体富化用）。
func (r *UserRepo) Username(ctx context.Context, id uint64) (string, error) {
	u, err := data.Client(ctx, r.data).User.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// RegisterInput 注册入参。
type RegisterInput struct {
	Username   string
	Password   string
	Email      string
	Phone      string
	InviteCode string
}

// Register 注册（归因链写入；username 唯一）。
func (r *UserRepo) Register(ctx context.Context, in RegisterInput) (*ent.User, error) {
	if len(in.Username) < 3 || len(in.Username) > 30 {
		return nil, errors.New("identity.USERNAME_INVALID: 3-30 字符")
	}
	if len(in.Password) < 6 {
		return nil, errors.New("identity.PASSWORD_TOO_SHORT: 至少 6 位")
	}
	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	// 归因链解析（双格式：8 位随机推广码 或 旧数字 user_id——兼容存量链接）
	var l1, l2, l3 uint64
	if in.InviteCode != "" {
		inviter := r.ResolvePromoCode(ctx, in.InviteCode)
		if inviter == nil || inviter.ID == 0 {
			return nil, errors.New("identity.INVITER_NOT_FOUND")
		}
		l1 = inviter.ID
		l2 = inviter.InviteL1
		l3 = inviter.InviteL2
	}
	create := data.Client(ctx, r.data).User.Create().
		SetUsername(in.Username).
		SetPasswordHash(hash).
		SetStatus(user.StatusActive).
		SetInviteL1(l1).SetInviteL2(l2).SetInviteL3(l3).
		SetPromoCode(genPromoCode())
	if in.Email != "" {
		create.SetEmail(in.Email)
	}
	if in.Phone != "" {
		create.SetPhone(in.Phone)
	}
	u, err := create.Save(ctx)
	if ent.IsConstraintError(err) {
		// 用户名冲突为主；推广码碰撞（概率极低）重试一次换码
		u, err = create.SetPromoCode(genPromoCode()).Save(ctx)
		if ent.IsConstraintError(err) {
			return nil, errors.New("identity.USERNAME_TAKEN")
		}
	}
	return u, err
}

// FindByUsername 按用户名查。
func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*ent.User, error) {
	u, err := data.Client(ctx, r.data).User.Query().
		Where(user.Username(username)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errors.New("identity.BAD_CREDENTIALS")
		}
		return nil, err
	}
	return u, nil
}

// FindByLoginIdentifier 登录混输（用户名/邮箱/手机号三标识一次查询——
// acg-faka 同款体验：单输入框自动识别）。唯一索引三选一，无歧义。
func (r *UserRepo) FindByLoginIdentifier(ctx context.Context, identifier string) (*ent.User, error) {
	u, err := data.Client(ctx, r.data).User.Query().
		Where(
			user.Or(
				user.Username(identifier),
				user.Email(identifier),
				user.Phone(identifier),
			),
		).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errors.New("identity.BAD_CREDENTIALS")
		}
		return nil, err
	}
	return u, nil
}

// Register API（security.register_method 驱动：username=免验证 / email=邮箱码 / phone=短信码）。
func (s *StoreUserService) Register(ctx context.Context, req *storefrontv1.RegisterRequest) (*storefrontv1.RegisterReply, error) {
	// 图形验证码（captcha_register 开启时前置）
	if s.captcha != nil {
		if err := s.captcha.VerifyScene(ctx, captcha.SceneRegister, req.GetCaptchaId(), req.GetCaptchaCode()); err != nil {
			return nil, err
		}
	}
	// 注册开关（security.register_enabled）
	if s.regCfg != nil && !s.regCfg.RegisterEnabled(ctx) {
		return nil, errors.New("identity.REGISTER_DISABLED: 站点暂未开放注册")
	}
	// 注册方式（多选）：email/phone 通道启用时对应目标必填 + 验码（一次性）
	if s.regCfg != nil {
		if s.regCfg.MethodEnabled(ctx, "email") {
			if req.GetEmail() == "" {
				return nil, errors.New("identity.EMAIL_REQUIRED: 请填写邮箱并完成验证")
			}
			if err := s.regCode.VerifyRegisterCode(ctx, req.GetEmail(), req.GetCode(), "email"); err != nil {
				return nil, err
			}
		}
		if s.regCfg.MethodEnabled(ctx, "phone") {
			if req.GetPhone() == "" {
				return nil, errors.New("identity.PHONE_REQUIRED: 请填写手机号并完成验证")
			}
			if err := s.regCode.VerifyRegisterCode(ctx, req.GetPhone(), req.GetCode(), "phone"); err != nil {
				return nil, err
			}
		}
	}
	u, err := s.repo.Register(ctx, RegisterInput{
		Username: req.GetUsername(), Password: req.GetPassword(),
		Email: req.GetEmail(), Phone: req.GetPhone(), InviteCode: req.GetInviteCode(),
	})
	if err != nil {
		return nil, err
	}
	token, exp, err := s.signer.Issue(authn.RealmUser, u.ID, u.Username, 0)
	if err != nil {
		return nil, err
	}
	return &storefrontv1.RegisterReply{UserId: u.ID, Token: token, ExpiresAt: exp.Unix(), PromoCode: u.PromoCode}, nil
}

// SendRegisterCode API（发码：60s 冷却 + 已注册拒绝 + 通道格式校验）。
func (s *StoreUserService) SendRegisterCode(ctx context.Context, req *storefrontv1.SendRegisterCodeRequest) (*storefrontv1.SendRegisterCodeReply, error) {
	// 图形验证码（captcha_register 场景——发码前防机器人，acg-faka 同款纪律）
	if s.captcha != nil {
		if err := s.captcha.VerifyScene(ctx, captcha.SceneRegister, req.GetCaptchaId(), req.GetCaptchaCode()); err != nil {
			return nil, err
		}
	}
	if err := s.regCode.SendRegisterCode(ctx, req.GetTarget(), req.GetChannel()); err != nil {
		return nil, err
	}
	return &storefrontv1.SendRegisterCodeReply{}, nil
}

// Login API（username 字段混输：用户名/邮箱/手机号）。
func (s *StoreUserService) Login(ctx context.Context, req *storefrontv1.LoginRequest) (*storefrontv1.LoginReply, error) {
	// 图形验证码（captcha_login 开启时前置；未启用直接放行）
	if s.captcha != nil {
		if err := s.captcha.VerifyScene(ctx, captcha.SceneLogin, req.GetCaptchaId(), req.GetCaptchaCode()); err != nil {
			return nil, err
		}
	}
	u, err := s.repo.FindByLoginIdentifier(ctx, req.GetUsername())
	if err != nil {
		return nil, err
	}
	if !crypto.VerifyPassword(u.PasswordHash, req.GetPassword()) {
		return nil, errors.New("identity.BAD_CREDENTIALS")
	}
	if string(u.Status) != "active" {
		return nil, errors.New("identity.USER_DISABLED")
	}
	token, exp, err := s.signer.Issue(authn.RealmUser, u.ID, u.Username, 0)
	if err != nil {
		return nil, err
	}
	return &storefrontv1.LoginReply{
		AccessToken: token, TokenType: "Bearer", ExpiresAt: exp.Unix(),
	}, nil
}

// Me API（登录态；推广码懒生成——存量用户首次访问即补）。
func (s *StoreUserService) Me(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.MeReply, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	u, err := data.Client(ctx, s.data).User.Get(ctx, claims.Subject)
	if err != nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	promoCode := u.PromoCode
	if promoCode == "" {
		promoCode = s.repo.EnsurePromoCode(ctx, u.ID)
	}
	reply := &storefrontv1.MeReply{
		UserId: u.ID, Username: u.Username, Email: u.Email,
		CreatedAt: u.CreatedAt.Unix(), PromoCode: promoCode,
	}
	// 分站主身份（approved）
	if p, err := data.Client(ctx, s.data).ResellerProfile.Query().
		Where(resellerprofile.UserID(u.ID), resellerprofile.StatusEQ(resellerprofile.StatusApproved)).
		Only(ctx); err == nil {
		reply.IsReseller = true
		reply.ResellerProfileId = p.ID
	}
	return reply, nil
}

// ── P3-10 自服务 API（薄 transport：委托 PasswordService）──────────

// ForgotPassword 发送找回密码验证码（防枚举：任何输入都成功）。
func (s *StoreUserService) ForgotPassword(ctx context.Context, req *storefrontv1.ForgotPasswordRequest) (*storefrontv1.ForgotPasswordReply, error) {
	// 图形验证码（captcha_reset 开启时前置——发码前防爆破）
	if s.captcha != nil {
		if err := s.captcha.VerifyScene(ctx, captcha.SceneReset, req.GetCaptchaId(), req.GetCaptchaCode()); err != nil {
			return nil, err
		}
	}
	if err := s.pwd.ForgotPassword(ctx, req.GetEmail()); err != nil {
		return nil, err
	}
	return &storefrontv1.ForgotPasswordReply{}, nil
}

// ResetPassword 验码重置（一次性；吊销 session；重置即登录）。
func (s *StoreUserService) ResetPassword(ctx context.Context, req *storefrontv1.ResetPasswordRequest) (*storefrontv1.ResetPasswordReply, error) {
	u, err := s.pwd.ResetPassword(ctx, req.GetEmail(), req.GetCode(), req.GetNewPassword())
	if err != nil {
		return nil, err
	}
	token, exp, err := s.signer.Issue(authn.RealmUser, u.ID, u.Username, 0)
	if err != nil {
		return nil, err
	}
	return &storefrontv1.ResetPasswordReply{Token: token, ExpiresAt: exp.Unix()}, nil
}

// ChangePassword 登录态改密（新 token 保当前会话）。
func (s *StoreUserService) ChangePassword(ctx context.Context, req *storefrontv1.ChangePasswordRequest) (*storefrontv1.ChangePasswordReply, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	u, err := s.pwd.ChangePassword(ctx, claims.Subject, req.GetOldPassword(), req.GetNewPassword())
	if err != nil {
		return nil, err
	}
	token, exp, err := s.signer.Issue(authn.RealmUser, u.ID, u.Username, 0)
	if err != nil {
		return nil, err
	}
	return &storefrontv1.ChangePasswordReply{Token: token, ExpiresAt: exp.Unix()}, nil
}

// UpdateProfile 改邮箱（Me 语义响应）。
func (s *StoreUserService) UpdateProfile(ctx context.Context, req *storefrontv1.UpdateProfileRequest) (*storefrontv1.MeReply, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	if err := s.pwd.UpdateProfile(ctx, claims.Subject, req.GetEmail()); err != nil {
		return nil, err
	}
	return s.Me(ctx, nil)
}
