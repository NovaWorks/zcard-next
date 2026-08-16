package settings

// 设置管理 API（薄 transport：校验 + 装配，业务在 biz）。
// 权限点：settings:read / settings:update（敏感分组如 security 的写入 M1 起加二次确认）。

import (
	"context"
	"encoding/json"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

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

// ListSettings 按分组列出（SECRET 键脱敏 ****）。
func (s *AdminSettingsService) ListSettings(ctx context.Context, req *adminv1.ListSettingsRequest) (*adminv1.ListSettingsReply, error) {
	items, err := s.uc.List(ctx, req.GetGroup())
	if err != nil {
		return nil, errors.InternalServer("settings.LIST_FAILED", "读取设置失败")
	}
	items = SanitizeGroup(items)
	reply := &adminv1.ListSettingsReply{Items: make([]*adminv1.Setting, 0, len(items))}
	for _, it := range items {
		reply.Items = append(reply.Items, &adminv1.Setting{Group: it.Group, Key: it.Key, ValueJson: string(it.Value)})
	}
	return reply, nil
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
	if err := s.uc.Put(ctx, req.GetGroup(), req.GetKey(), value); err != nil {
		return nil, errors.BadRequest("settings.INVALID_VALUE", "设置值必须是合法 JSON")
	}
	return &adminv1.Setting{Group: req.GetGroup(), Key: req.GetKey(), ValueJson: string(value)}, nil
}
