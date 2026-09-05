package notify

// wire providers（）。
//
// 跨模块绑定（通道 A）：
// - EmailChannel.settings ← notifyport.SettingsReader（settings.RepoImpl 适配，见下）
// - Dispatcher 实现 notifyport.Sender（业务模块告警直调）

import (
	"context"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// settingsReaderAdapter settings.RepoImpl → notifyport.SettingsReader 适配。
type settingsReaderAdapter struct{ repo *settings.RepoImpl }

func (a settingsReaderAdapter) GetJSON(ctx context.Context, group, key string) ([]byte, error) {
	raw, err := a.repo.GetDefault(ctx, group, key, nil)
	if err != nil {
		return nil, nil // 读失败走降级（skipped），不阻断通知链路
	}
	return raw, nil
}

// ProviderSet notify providers。
var ProviderSet = wire.NewSet(
	NewNotifyRepo,
	ProvideChannels,
	ProvideSettingsReader, // notifyport.SettingsReader 绑定（order 互斥开关等消费）
	ProvideDispatcher,
	wire.Bind(new(notifyport.Sender), new(*Dispatcher)), // audit Alerter 消费（通道 A）
	NewBroadcastService,
	NewAdminNotifyService,
	NewStoreNotificationService,
)

// ProvideDispatcher 构造分发器并装配白标解析（BrandResolver 由 reseller 提供，
// 通道 A；nil = 未装配跳过品牌注入）。
func ProvideDispatcher(repo *NotifyRepo, channels []Channel, brand notifyport.BrandResolver) *Dispatcher {
	return NewDispatcher(repo, channels...).WithBrandResolver(brand)
}

// ProvideSettingsReader settings 适配为通用端口（跨模块共享，通道 A）。
func ProvideSettingsReader(repo *settings.RepoImpl) notifyport.SettingsReader {
	return settingsReaderAdapter{repo: repo}
}

// ProvideChannels 通道装配（四通道：Email/Inbox/SMS/Telegram）。
func ProvideChannels(repo *NotifyRepo, settingsRepo *settings.RepoImpl) []Channel {
	reader := settingsReaderAdapter{repo: settingsRepo}
	return []Channel{
		NewEmailChannel(reader),
		NewInboxChannel(repo),
		NewSMSChannel(reader),
		NewTelegramChannel(reader),
	}
}
