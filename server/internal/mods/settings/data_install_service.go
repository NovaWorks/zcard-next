package settings

// 在线安装服务（Web /install 表单后端）：复用 Install()（单事务种子 + 管理员）。
// Public 端点——仅未安装时可写；已安装后 status 只读、install 幂等拒绝（409）。

import (
	"context"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// serverVersion 构建注入（http.server 同源 Version；链接期由 main 设置）。
var serverVersion = "dev"

// SetServerVersion 供启动链注入构建版本（cmd main 调用）。
func SetServerVersion(v string) { serverVersion = v }

// AdminInstallService 在线安装服务。
type AdminInstallService struct {
	adminv1.UnimplementedAdminInstallServiceServer
	data *data.Data
}

// NewAdminInstallService 构造。
func NewAdminInstallService(d *data.Data) *AdminInstallService {
	return &AdminInstallService{data: d}
}

// GetInstallStatus 安装状态 + 环境自检（公开只读）。
func (s *AdminInstallService) GetInstallStatus(ctx context.Context, _ *emptypb.Empty) (*adminv1.InstallStatusReply, error) {
	installed := Installed(ctx, s.data)
	reply := &adminv1.InstallStatusReply{
		Installed:    installed,
		Dialect:      string(s.data.Dialect),
		Version:      serverVersion,
		DatabaseOk:   s.data.DB != nil,
		MigrationsOk: true, // 迁移失败启动即拒绝（fail-fast），能应答即已应用
	}
	if installed {
		if ts, err := installedAt(ctx, s.data); err == nil {
			reply.InstalledAt = ts
		}
	}
	return reply, nil
}

// PerformInstall 执行安装（未安装可写；已安装 409 幂等拒绝）。
//
// dialect=sqlite（或缺省）：当前库直装（零依赖体验不变）；
// dialect=mysql/postgres：校验连接（库不存在自动建）→ 写覆盖配置 + 待装凭据
// → 响应 restart_required 并自重启（新库迁移后补装完成；前端轮询至 installed）。
func (s *AdminInstallService) PerformInstall(ctx context.Context, req *adminv1.PerformInstallRequest) (*adminv1.InstallStatusReply, error) {
	if Installed(ctx, s.data) {
		return nil, errors.Conflict("settings.ALREADY_INSTALLED", "系统已安装（如需重装请清空数据库）")
	}
	admin := InstallInput{
		AdminUsername: req.GetAdminUsername(),
		AdminPassword: req.GetAdminPassword(),
		SiteName:      req.GetSiteName(),
		SiteURL:       req.GetSiteUrl(),
	}
	// 库切换分支：mysql/postgres → 校验 + 落盘 + 自重启（不在当前库安装）
	if d := req.GetDialect(); d == "mysql" || d == "postgres" {
		sw := dbSwitchInput{
			Dialect: d, Host: req.GetDbHost(), Port: req.GetDbPort(),
			User: req.GetDbUser(), Password: req.GetDbPassword(), Name: req.GetDbName(),
			RedisAddr: req.GetRedisAddr(), RedisPassword: req.GetRedisPassword(),
		}
		if err := validateSwitch(ctx, sw); err != nil {
			return nil, errors.BadRequest("settings.DB_SWITCH_INVALID", err.Error())
		}
		// 管理员入参先行校验（弱密码不落盘、不重启）
		if len(admin.AdminPassword) < 8 {
			return nil, errors.BadRequest("settings.WEAK_PASSWORD", "管理员密码至少 8 位")
		}
		if err := writeSwitchFiles(sw, admin); err != nil {
			return nil, errors.InternalServer("settings.DB_SWITCH_FAILED", err.Error())
		}
		scheduleSelfRestart(800 * time.Millisecond)
		return &adminv1.InstallStatusReply{Installed: false, RestartRequired: true,
			Dialect: d, Version: serverVersion, DatabaseOk: true, MigrationsOk: true}, nil
	}
	if err := Install(ctx, s.data, admin); err != nil {
		return nil, errors.BadRequest("settings.INSTALL_FAILED", err.Error())
	}
	return s.GetInstallStatus(ctx, nil)
}

// TestInstallConnection 测试目标库/Redis 连通（向导「测试连接」；库不存在自动建）。
func (s *AdminInstallService) TestInstallConnection(ctx context.Context, req *adminv1.TestInstallConnectionRequest) (*adminv1.TestInstallConnectionReply, error) {
	sw := dbSwitchInput{
		Dialect: req.GetDialect(), Host: req.GetDbHost(), Port: req.GetDbPort(),
		User: req.GetDbUser(), Password: req.GetDbPassword(), Name: req.GetDbName(),
		RedisAddr: req.GetRedisAddr(), RedisPassword: req.GetRedisPassword(),
	}
	if err := validateSwitch(ctx, sw); err != nil {
		return &adminv1.TestInstallConnectionReply{Ok: false, Message: err.Error()}, nil
	}
	return &adminv1.TestInstallConnectionReply{Ok: true, Message: "数据库与 Redis 连接成功"}, nil
}
