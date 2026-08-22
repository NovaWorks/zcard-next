package settings

// 在线安装服务（Web /install 表单后端）：复用 Install()（单事务种子 + 管理员）。
// Public 端点——仅未安装时可写；已安装后 status 只读、install 幂等拒绝（409）。

import (
	"context"

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
		Installed:   installed,
		Dialect:     string(s.data.Dialect),
		Version:     serverVersion,
		DatabaseOk:  s.data.DB != nil,
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
func (s *AdminInstallService) PerformInstall(ctx context.Context, req *adminv1.PerformInstallRequest) (*adminv1.InstallStatusReply, error) {
	if Installed(ctx, s.data) {
		return nil, errors.Conflict("settings.ALREADY_INSTALLED", "系统已安装（如需重装请清空数据库）")
	}
	if err := Install(ctx, s.data, InstallInput{
		AdminUsername: req.GetAdminUsername(),
		AdminPassword: req.GetAdminPassword(),
		SiteName:      req.GetSiteName(),
		SiteURL:       req.GetSiteUrl(),
	}); err != nil {
		return nil, errors.BadRequest("settings.INSTALL_FAILED", err.Error())
	}
	return s.GetInstallStatus(ctx, nil)
}
