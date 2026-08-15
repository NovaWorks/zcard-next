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

	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
)

// ErrSettingNotFound 设置项不存在。
var ErrSettingNotFound = errors.New("settings.NOT_FOUND")

// Repo 设置仓储（模块内端口，实现于 data.go）。
type Repo interface {
	Get(ctx context.Context, group, key string) (json.RawMessage, error)
	List(ctx context.Context, group string) ([]port.Item, error)
	Put(ctx context.Context, group, key string, value json.RawMessage) error
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

// Put 更新单项（value 服务端校验为合法 JSON 文档后落库）。
func (uc *SettingsUsecase) Put(ctx context.Context, group, key string, value json.RawMessage) error {
	if !json.Valid(value) {
		return errors.New("settings.INVALID_VALUE")
	}
	return uc.repo.Put(ctx, group, key, value)
}
