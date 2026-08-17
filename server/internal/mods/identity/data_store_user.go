package identity

// StoreUserService 用户中心（P3-04 前置）：注册/登录/Me（user realm JWT）。
// 归因链：invite_code=<user_id> → l1=邀请人 l2=邀请人.l1 l3=邀请人.l2（环状拒绝）。

import (
	"context"
	"errors"
	"strconv"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
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
	repo   *UserRepo
	signer *authn.Signer
	data   *data.Data
	pwd    *PasswordService // P3-10 自服务（找回/改密/改资料）
}

// NewStoreUserService 构造。
func NewStoreUserService(repo *UserRepo, signer *authn.Signer, d *data.Data, pwd *PasswordService) *StoreUserService {
	return &StoreUserService{repo: repo, signer: signer, data: d, pwd: pwd}
}

// UserRepo 用户仓储。
type UserRepo struct{ data *data.Data }

// NewUserRepo 构造。
func NewUserRepo(d *data.Data) *UserRepo { return &UserRepo{data: d} }

// RegisterInput 注册入参。
type RegisterInput struct {
	Username   string
	Password   string
	Email      string
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
	// 归因链解析（环状拒绝：invite 指向自己）
	var l1, l2, l3 uint64
	if in.InviteCode != "" {
		inviterID, err := strconv.ParseUint(in.InviteCode, 10, 64)
		if err != nil || inviterID == 0 {
			return nil, errors.New("identity.INVITE_CODE_INVALID")
		}
		inviter, err := data.Client(ctx, r.data).User.Get(ctx, inviterID)
		if err != nil {
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
		SetInviteL1(l1).SetInviteL2(l2).SetInviteL3(l3)
	if in.Email != "" {
		create.SetEmail(in.Email)
	}
	u, err := create.Save(ctx)
	if ent.IsConstraintError(err) {
		return nil, errors.New("identity.USERNAME_TAKEN")
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

// Register API。
func (s *StoreUserService) Register(ctx context.Context, req *storefrontv1.RegisterRequest) (*storefrontv1.RegisterReply, error) {
	u, err := s.repo.Register(ctx, RegisterInput{
		Username: req.GetUsername(), Password: req.GetPassword(),
		Email: req.GetEmail(), InviteCode: req.GetInviteCode(),
	})
	if err != nil {
		return nil, err
	}
	token, exp, err := s.signer.Issue(authn.RealmUser, u.ID, u.Username, 0)
	if err != nil {
		return nil, err
	}
	return &storefrontv1.RegisterReply{UserId: u.ID, Token: token, ExpiresAt: exp.Unix()}, nil
}

// Login API。
func (s *StoreUserService) Login(ctx context.Context, req *storefrontv1.LoginRequest) (*storefrontv1.LoginReply, error) {
	u, err := s.repo.FindByUsername(ctx, req.GetUsername())
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

// Me API（登录态）。
func (s *StoreUserService) Me(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.MeReply, error) {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	u, err := data.Client(ctx, s.data).User.Get(ctx, claims.Subject)
	if err != nil {
		return nil, errors.New("identity.UNAUTHORIZED")
	}
	reply := &storefrontv1.MeReply{
		UserId: u.ID, Username: u.Username, Email: u.Email,
		CreatedAt: u.CreatedAt.Unix(),
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
