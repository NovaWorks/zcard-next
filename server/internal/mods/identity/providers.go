package identity

// wire providers（模块自装配；跨模块绑定在 bootstrap）。

import (
	"context"
	"encoding/json"

	"github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"

	"github.com/google/wire"
)

// ProviderSet identity providers。
var ProviderSet = wire.NewSet(
	NewIdentityUsecase,
	NewAdminUserRepoImpl,
	wire.Bind(new(AdminUserRepo), new(*AdminUserRepoImpl)),
	wire.Bind(new(port.AdminMutator), new(*AdminUserRepoImpl)),
	wire.Bind(new(port.AdminReader), new(*AdminUserRepoImpl)),
	NewUserRepo,
	wire.Bind(new(port.UserReader), new(*UserRepo)),
	NewAdminAuthService,	NewPasswordService,
	NewRegisterCodeService,
	ProvideRegisterCodeSettings, // 注册开关/方式读取（复用 notifyport.SettingsReader 绑定）
	NewStoreUserService,
	NewAdminUserManageService,
)

// RegisterCodeSettings security 组设置读取（通道 A：settings 适配的
// notifyport.SettingsReader——避免 identity→settings 直接依赖产生环）。
type RegisterCodeSettings struct {
	read notifyport.SettingsReader
}

// ProvideRegisterCodeSettings 构造。
func ProvideRegisterCodeSettings(read notifyport.SettingsReader) *RegisterCodeSettings {
	return &RegisterCodeSettings{read: read}
}

// RegisterMethods 注册方式（多选数组：["username","email","phone"]；兼容旧单值字符串）。
// 返回勾选的通道列表；空/解析失败回落 ["username"]。
func (s *RegisterCodeSettings) RegisterMethods(ctx context.Context) []string {
	raw, err := s.read.GetJSON(ctx, "security", "register_method")
	if err != nil || len(raw) == 0 {
		return []string{"username"}
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return arr
	}
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one} // 旧单值兼容
	}
	return []string{"username"}
}

// MethodEnabled 指定通道是否启用（多选语义）。
func (s *RegisterCodeSettings) MethodEnabled(ctx context.Context, m string) bool {
	for _, v := range s.RegisterMethods(ctx) {
		if v == m {
			return true
		}
	}
	return false
}

// AdminLoginCaptcha 后台登录验证码（security.captcha_admin_login；默认 false）。
func (s *RegisterCodeSettings) AdminLoginCaptcha(ctx context.Context) bool {
	raw, err := s.read.GetJSON(ctx, "security", "captcha_admin_login")
	if err != nil || len(raw) == 0 {
		return false
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return v
}

// RegisterEnabled 开放注册（security.register_enabled；默认 true）。
func (s *RegisterCodeSettings) RegisterEnabled(ctx context.Context) bool {
	raw, err := s.read.GetJSON(ctx, "security", "register_enabled")
	if err != nil || len(raw) == 0 {
		return true
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	return v
}
