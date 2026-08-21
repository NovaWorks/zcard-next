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
	"sort"

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
	// CurrencyExists 货币是否存在（i18n.base_currency 写入校验）。
	CurrencyExists(ctx context.Context, code string) (bool, error)
	// PutMany 批量写入（事务原子）。
	PutMany(ctx context.Context, items []port.Item) error
}

// SettingsUsecase 设置用例。
type SettingsUsecase struct {
	repo Repo
}

// NewSettingsUsecase 构造。
func NewSettingsUsecase(repo Repo) *SettingsUsecase { return &SettingsUsecase{repo: repo} }

// CurrencyExists 转发（service 层 base_currency 写入校验）。
func (uc *SettingsUsecase) CurrencyExists(ctx context.Context, code string) (bool, error) {
	return uc.repo.CurrencyExists(ctx, code)
}

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

// PutMany 批量更新（表单级保存；单事务原子写入——任一项失败整体回滚）。
// 校验语义与 Put 一致；SECRET 键 **** 回写跳过。
func (uc *SettingsUsecase) PutMany(ctx context.Context, items []port.Item) error {
	if len(items) == 0 {
		return nil
	}
	valid := make([]port.Item, 0, len(items))
	for _, it := range items {
		if err := ValidateKey(it.Group, it.Key); err != nil {
			return err
		}
		if !json.Valid(it.Value) {
			return errors.New("settings.INVALID_VALUE")
		}
		if IsSecret(it.Group, it.Key) && string(it.Value) == `"****"` {
			continue
		}
		valid = append(valid, it)
	}
	if len(valid) == 0 {
		return nil
	}
	return uc.repo.PutMany(ctx, valid)
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

// withDefaults 目录默认值补齐（admin 列表用）：DB 已写入的键优先，
// 未写入的键以目录默认 JSON 展示——保证每个分组全量可见
// （与前台 GetPublicConfig 的回落逻辑同源；保存后即落 DB 行）。
func withDefaults(groupFilter string, items []port.Item) []port.Item {
	have := make(map[string]bool, len(items))
	for _, it := range items {
		have[it.Group+"."+it.Key] = true
	}
	var extra []port.Item
	for _, gname := range GroupsSorted() {
		if groupFilter != "" && gname != groupFilter {
			continue
		}
		g, ok := Group(gname)
		if !ok {
			continue
		}
		for k := range g.Defaults {
			if have[gname+"."+k] {
				continue
			}
			if def, has := g.DefaultJSON(k); has {
				extra = append(extra, port.Item{Group: gname, Key: k, Value: json.RawMessage(def)})
			}
		}
	}
	if len(extra) == 0 {
		return items
	}
	out := append(items, extra...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Key < out[j].Key
	})
	return out
}
