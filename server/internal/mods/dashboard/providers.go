package dashboard

// wire providers。

import (
	"context"

	dashboardport "github.com/NovaWorks/zcard-next/server/internal/mods/dashboard/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// settingsReaderAdapter settings.RepoImpl → dashboardport.SettingsReader 适配
// （低库存阈值读取；读失败走默认值，不阻断工作台统计）。
type settingsReaderAdapter struct{ repo *settings.RepoImpl }

func (a settingsReaderAdapter) GetJSON(ctx context.Context, group, key string) ([]byte, error) {
	raw, err := a.repo.GetDefault(ctx, group, key, nil)
	if err != nil {
		return nil, nil
	}
	return raw, nil
}

// ProvideSettingsReader settings 适配为通用端口（通道 A）。
func ProvideSettingsReader(repo *settings.RepoImpl) dashboardport.SettingsReader {
	return settingsReaderAdapter{repo: repo}
}

// ProviderSet dashboard providers。
var ProviderSet = wire.NewSet(
	NewDashboardRepoImpl,
	NewReconciler,
	NewAdminDashboardService,
	ProvideSettingsReader, // dashboardport.SettingsReader 绑定（低库存阈值等）
)
