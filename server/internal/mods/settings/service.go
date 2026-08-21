package settings

// 设置管理 API（薄 transport：校验 + 装配，业务在 biz）。
// 权限点：settings:read / settings:update（敏感分组如 security 的写入 M1 起加二次确认）。

import (
	"context"
	"encoding/json"
	"sort"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/go-kratos/kratos/v3/errors"
)

// 权限点。
const (
	PermRead   = "settings:read"
	PermUpdate = "settings:update"
)

// AdminSettingsService 设置管理服务（实现 adminv1.AdminSettingsService）。
type AdminSettingsService struct {
	adminv1.UnimplementedAdminSettingsServiceServer
	uc *SettingsUsecase
}

// NewAdminSettingsService 构造。
func NewAdminSettingsService(uc *SettingsUsecase) *AdminSettingsService {
	return &AdminSettingsService{uc: uc}
}

// ListSettings 按分组列出（目录默认值补齐，未写入键以默认 JSON 展示；
// SECRET 键脱敏 ****）。
func (s *AdminSettingsService) ListSettings(ctx context.Context, req *adminv1.ListSettingsRequest) (*adminv1.ListSettingsReply, error) {
	items, err := s.uc.List(ctx, req.GetGroup())
	if err != nil {
		return nil, errors.InternalServer("settings.LIST_FAILED", "读取设置失败")
	}
	items = withDefaults(req.GetGroup(), items)
	items = SanitizeGroup(items)
	reply := &adminv1.ListSettingsReply{Items: make([]*adminv1.Setting, 0, len(items))}
	for _, it := range items {
		label := it.Key
		var options []*adminv1.OptionItem
		secret := false
		if g, ok := Group(it.Group); ok {
			if l, has := g.Labels[it.Key]; has {
				label = l
			}
			for v, l := range g.Options[it.Key] {
				options = append(options, &adminv1.OptionItem{Value: v, Label: l})
			}
			sort.Slice(options, func(i, j int) bool { return options[i].Value < options[j].Value })
			secret = g.SecretKeys[it.Key]
		}
		reply.Items = append(reply.Items, &adminv1.Setting{
			Group: it.Group, Key: it.Key, ValueJson: string(it.Value), Label: label,
			Options: options, Secret: secret,
		})
	}
	return reply, nil
}

// validateSettingValue 设置值业务校验（单键/批量共用）：
// base_currency 必须存在、模板键必须在清单内。
func (s *AdminSettingsService) validateSettingValue(ctx context.Context, group, key string, value json.RawMessage) error {
	// 基础货币必须指向货币表中已存在的货币（防孤儿配置——前台按 code 取符号）。
	if group == "i18n" && key == "base_currency" {
		var code string
		if err := json.Unmarshal(value, &code); err != nil || code == "" {
			return errors.BadRequest("settings.INVALID_VALUE", "base_currency 必须是非空货币代码字符串")
		}
		ok, err := s.uc.CurrencyExists(ctx, code)
		if err != nil {
			return errors.InternalServer("settings.CURRENCY_CHECK_FAILED", "校验货币失败")
		}
		if !ok {
			return errors.BadRequest("settings.CURRENCY_NOT_FOUND", "货币不存在，请先在「货币」页创建")
		}
	}
	// 模板键必须存在于可用模板清单（防写不存在的模板 key）。
	if group == "template" && (key == "pc_template" || key == "mobile_template") {
		var tk string
		if err := json.Unmarshal(value, &tk); err != nil || tk == "" {
			return errors.BadRequest("settings.INVALID_VALUE", "模板键必须是非空字符串")
		}
		if !templateKeyExists(tk) {
			return errors.BadRequest("settings.TEMPLATE_NOT_FOUND", "模板不存在，请从模板清单中选择")
		}
	}
	return nil
}

// GetSetting 读取单项（SECRET 键脱敏）。
func (s *AdminSettingsService) GetSetting(ctx context.Context, req *adminv1.GetSettingRequest) (*adminv1.Setting, error) {
	if err := ValidateKey(req.GetGroup(), req.GetKey()); err != nil {
		return nil, errors.NotFound("settings.NOT_FOUND", "设置项不存在或不在目录内")
	}
	v, err := s.uc.Get(ctx, req.GetGroup(), req.GetKey())
	if err != nil {
		return nil, errors.NotFound("settings.NOT_FOUND", "设置项不存在")
	}
	val := string(v)
	if IsSecret(req.GetGroup(), req.GetKey()) {
		val = `"****"`
	}
	return &adminv1.Setting{Group: req.GetGroup(), Key: req.GetKey(), ValueJson: val}, nil
}

// UpdateSetting 更新单项。
func (s *AdminSettingsService) UpdateSetting(ctx context.Context, req *adminv1.UpdateSettingRequest) (*adminv1.Setting, error) {
	value := json.RawMessage(req.GetValueJson())
	if err := s.validateSettingValue(ctx, req.GetGroup(), req.GetKey(), value); err != nil {
		return nil, err
	}
	if err := s.uc.Put(ctx, req.GetGroup(), req.GetKey(), value); err != nil {
		return nil, errors.BadRequest("settings.INVALID_VALUE", "设置值必须是合法 JSON")
	}
	return &adminv1.Setting{Group: req.GetGroup(), Key: req.GetKey(), ValueJson: string(value)}, nil
}

// UpdateSettings 批量更新（表单级保存；单事务原子写入，任一项失败整体回滚）。
func (s *AdminSettingsService) UpdateSettings(ctx context.Context, req *adminv1.UpdateSettingsRequest) (*adminv1.UpdateSettingsReply, error) {
	items := make([]port.Item, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		value := json.RawMessage(it.GetValueJson())
		if err := s.validateSettingValue(ctx, it.GetGroup(), it.GetKey(), value); err != nil {
			return nil, err
		}
		items = append(items, port.Item{Group: it.GetGroup(), Key: it.GetKey(), Value: value})
	}
	if err := s.uc.PutMany(ctx, items); err != nil {
		return nil, errors.BadRequest("settings.INVALID_VALUE", "设置值必须是合法 JSON 或键不在目录内")
	}
	return &adminv1.UpdateSettingsReply{Updated: int32(len(items))}, nil
}
