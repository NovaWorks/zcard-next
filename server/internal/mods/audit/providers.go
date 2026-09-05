package audit

// wire providers（）。
//
// 跨模块绑定（通道 A）：
// - AuditRepo 实现 auditport.Auditor（identity/inventory/authz 埋点）
// - AuditRepo 实现 auditport.RiskGate（order 下单闸门 / fulfillment 取货锁定）

import (
	"context"
	"log/slog"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// ProviderSet audit providers。
// 告警：Alerter 依赖 notifyport.Sender（notify.Dispatcher 实现）+
// notifyport.SettingsReader（settings 适配——经 NotifySettingsBridge 注入）。
var ProviderSet = wire.NewSet(
	NewVisitCounter,
	NewTrackRepo,
	wire.Bind(new(port.TrafficReader), new(*TrackRepo)),
	ProvideAuditAlerter,
	wire.Bind(new(port.Auditor), new(*AuditRepo)),
	wire.Bind(new(port.RiskGate), new(*AuditRepo)),
	NewAdminAuditService,
	NewAuditRepoWithAlerter,
)

// auditSettingsAdapter settings.RepoImpl → notifyport.SettingsReader（audit 复用）。
type auditSettingsAdapter struct{ repo *settings.RepoImpl }

func (a auditSettingsAdapter) GetJSON(ctx context.Context, group, key string) ([]byte, error) {
	raw, err := a.repo.GetDefault(ctx, group, key, nil)
	if err != nil {
		return nil, nil
	}
	return raw, nil
}

// ProvideAuditAlerter 构造告警器（settings + notify sender）。
func ProvideAuditAlerter(repo *settings.RepoImpl, sender notifyport.Sender) *Alerter {
	return NewAlerter(auditSettingsAdapter{repo: repo}, sender)
}

// NewAuditRepoWithAlerter 构造仓储并注入告警器（单一 provider 合并依赖）。
func NewAuditRepoWithAlerter(d *data.Data, logger *slog.Logger, alerter *Alerter) *AuditRepo {
	r := NewAuditRepo(d, logger)
	r.SetAlerter(alerter)
	return r
}
