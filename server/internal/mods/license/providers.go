package license

// wire providers（）。

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NovaWorks/zcard-next/server/internal/mods/license/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// ProviderSet license providers。
var ProviderSet = wire.NewSet(
	ProvideLicenseRepo,
	wire.Bind(new(port.InstanceIdentity), new(*LicenseRepo)),
	NewAdminLicenseService,
	// ：专业套餐在线购买（storefront 面）
	NewPurchaseRepo,
	NewStoreLicenseService,
)

// ProvideLicenseRepo 装配（设置读写经 *settings.RepoImpl——通道 A，wire 注入）。
func ProvideLicenseRepo(repo *settings.RepoImpl) *LicenseRepo {
	return NewLicenseRepo(identitySettings{repo})
}

// Missing identity may be initialized; a failed read must never replace it.
type identitySettings struct{ *settings.RepoImpl }

func (r identitySettings) Get(ctx context.Context, group, key string) (json.RawMessage, error) {
	raw, err := r.RepoImpl.Get(ctx, group, key)
	if errors.Is(err, settings.ErrSettingNotFound) {
		return nil, nil
	}
	return raw, err
}
