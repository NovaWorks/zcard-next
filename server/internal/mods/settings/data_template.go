package settings

import (
	"context"
	"encoding/base64"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *AdminSettingsService) ListTemplates(_ context.Context, _ *emptypb.Empty) (*adminv1.TemplateList, error) {
	items, err := scanTemplates()
	if err != nil {
		return nil, err
	}
	return &adminv1.TemplateList{Templates: items}, nil
}
func scanTemplates() ([]*adminv1.TemplateItem, error) {
	out := []*adminv1.TemplateItem{{Key: "classic", Name: "Classic", Desc: "内置响应式主题（PC / 手机自适应）", Author: "ZCard Team", Version: "1.0.0"}}
	themes, err := theme.List()
	if err != nil {
		return nil, err
	}
	for _, t := range themes {
		out = append(out, templateItem(t))
	}
	return out, nil
}
func templateItem(t *theme.Theme) *adminv1.TemplateItem {
	preview := ""
	if t.Preview != "" {
		preview = t.BaseURL() + t.Preview
	}
	return &adminv1.TemplateItem{Key: t.Key, Name: t.Name, Desc: t.Desc, Preview: preview, Author: t.Author, Version: t.Version}
}
func templateKeyExists(key string) bool {
	if key == "classic" {
		return true
	}
	_, err := theme.Resolve(key)
	return err == nil
}
func (s *AdminSettingsService) InstallTemplate(ctx context.Context, req *adminv1.InstallTemplateRequest) (*adminv1.TemplateItem, error) {
	if len(req.GetDataBase64()) > base64.StdEncoding.EncodedLen(theme.MaxZipBytes) {
		return nil, errors.BadRequest("settings.TEMPLATE_TOO_LARGE", "主题包超过 20MB 上限")
	}
	raw, err := base64.StdEncoding.DecodeString(req.GetDataBase64())
	if err != nil {
		return nil, errors.BadRequest("settings.TEMPLATE_BAD_ENCODING", "主题文件编码非法")
	}
	themeChanges.Lock()
	defer themeChanges.Unlock()
	if err := s.freezeLegacyTheme(ctx); err != nil {
		return nil, errors.InternalServer("settings.TEMPLATE_PIN_FAILED", "保留当前主题版本失败，未安装新主题，请重试")
	}
	t, err := theme.Install(raw)
	if err != nil {
		return nil, errors.BadRequest("settings.TEMPLATE_INVALID", err.Error())
	}
	return templateItem(t), nil
}
