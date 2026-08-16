// Package settings 设置中心（M0）：settings 表 = 运行时业务开关真理源。
//
// 分组目录（规划 §5.15）：site（基础）/template（模板）/trade（交易）/security（安全）/
// ops（运维）/notify（邮件短信）/recharge（充值提现）/points（积分）/affiliate（分销）/
// supply（货源）/i18n（语言货币）。安装向导 /install M1 交付。
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
)

// ErrSettingNotFound 设置项不存在。
var ErrSettingNotFound = errors.New("settings.NOT_FOUND")

// Repo 设置仓储（模块内端口，实现于 data.go）。
type Repo interface {
	Get(ctx context.Context, group, key string) (json.RawMessage, error)
	List(ctx context.Context, group string) ([]port.Item, error)
	Put(ctx context.Context, group, key string, value json.RawMessage) error
	// Currencies 启用货币视图（前台列表）。
	Currencies(ctx context.Context) ([]CurrencyView, error)
}

// SettingsUsecase 设置用例。
type SettingsUsecase struct {
	repo Repo
}

// NewSettingsUsecase 构造。
func NewSettingsUsecase(repo Repo) *SettingsUsecase { return &SettingsUsecase{repo: repo} }

// Get 读取单项。
func (uc *SettingsUsecase) Get(ctx context.Context, group, key string) (json.RawMessage, error) {
	return uc.repo.Get(ctx, group, key)
}

// GetDefault 读取单项，不存在返回默认值。
func (uc *SettingsUsecase) GetDefault(ctx context.Context, group, key string, def json.RawMessage) (json.RawMessage, error) {
	v, err := uc.repo.Get(ctx, group, key)
	if errors.Is(err, ErrSettingNotFound) {
		return def, nil
	}
	return v, err
}

// List 按分组列出（group 为空列出全部）。
func (uc *SettingsUsecase) List(ctx context.Context, group string) ([]port.Item, error) {
	return uc.repo.List(ctx, group)
}

// Put 更新单项（键经目录校验 + JSON 合法性；SECRET 键回写 **** 视为未修改跳过）。
func (uc *SettingsUsecase) Put(ctx context.Context, group, key string, value json.RawMessage) error {
	if err := ValidateKey(group, key); err != nil {
		return err
	}
	if !json.Valid(value) {
		return errors.New("settings.INVALID_VALUE")
	}
	if IsSecret(group, key) && string(value) == `"****"` {
		return nil // 脱敏回写 = 未修改
	}
	return uc.repo.Put(ctx, group, key, value)
}

// GetStruct 泛型读取（P0-04 T1）：JSON 绑定到调用方结构体，DB 缺省回落目录默认值。
// 用法：var cfg TradeConfig; uc.GetStruct(ctx, "trade", "guest_checkout", &cfg)
func GetStruct[T any](ctx context.Context, uc *SettingsUsecase, group, key string, out *T) error {
	raw, err := uc.GetDefault(ctx, group, key, nil)
	if err != nil {
		return err
	}
	if raw == nil {
		// DB 无值 → 用目录默认值
		g, ok := Group(group)
		if !ok {
			return fmt.Errorf("settings: 未知分组 %q", group)
		}
		def, has := g.DefaultJSON(key)
		if !has {
			return fmt.Errorf("settings: 分组 %q 内未知键 %q", group, key)
		}
		raw = json.RawMessage(def)
	}
	return json.Unmarshal(raw, out)
}

// SanitizeGroup 脱敏分组（列表/详情接口用）：SECRET 键值替换 ****。
func SanitizeGroup(items []port.Item) []port.Item {
	out := make([]port.Item, 0, len(items))
	for _, it := range items {
		if IsSecret(it.Group, it.Key) {
			out = append(out, port.Item{Group: it.Group, Key: it.Key, Value: json.RawMessage(`"****"`)})
			continue
		}
		out = append(out, it)
	}
	return out
}
