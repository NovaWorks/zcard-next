package settings

import (
	"context"
	"encoding/json"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"google.golang.org/protobuf/types/known/emptypb"
	"testing"
)

type themeMemoryRepo struct{ values map[string]json.RawMessage }

func (r *themeMemoryRepo) Get(_ context.Context, g, k string) (json.RawMessage, error) {
	v, ok := r.values[g+"."+k]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return v, nil
}
func (r *themeMemoryRepo) List(_ context.Context, g string) ([]port.Item, error) {
	out := []port.Item{}
	for _, key := range []string{"pc_template", "mobile_template"} {
		if v, ok := r.values["template."+key]; ok {
			out = append(out, port.Item{Group: "template", Key: key, Value: v})
		}
	}
	return out, nil
}
func (r *themeMemoryRepo) Put(_ context.Context, g, k string, v json.RawMessage) error {
	r.values[g+"."+k] = v
	return nil
}
func (r *themeMemoryRepo) PutMany(ctx context.Context, items []port.Item) error {
	for _, it := range items {
		r.Put(ctx, it.Group, it.Key, it.Value)
	}
	return nil
}
func (r *themeMemoryRepo) Currencies(context.Context) ([]CurrencyView, error)   { return nil, nil }
func (r *themeMemoryRepo) CurrencyExists(context.Context, string) (bool, error) { return true, nil }
func TestUnifiedThemeReadWrite(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	r := &themeMemoryRepo{values: map[string]json.RawMessage{"template.mobile_template": json.RawMessage(`"old-mobile"`)}}
	svc := NewAdminSettingsService(NewSettingsUsecase(r))
	if svc.ActiveTemplate(ctx) != "old-mobile" {
		t.Fatal("missing PC must preserve mobile-only legacy selection")
	}
	r.values["template.pc_template"] = json.RawMessage(`"classic"`)
	if svc.ActiveTemplate(ctx) != "classic" {
		t.Fatal("PC selection must take priority, including explicit Classic")
	}
	legacy, err := svc.GetSetting(ctx, &adminv1.GetSettingRequest{Group: "template", Key: "mobile_template"})
	if err != nil || legacy.ValueJson != `"classic"` {
		t.Fatal("legacy single read diverged from active theme", err)
	}
	list, e := svc.ListSettings(ctx, &adminv1.ListSettingsRequest{Group: "template"})
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, it := range list.Items {
		if it.Key == "mobile_template" {
			t.Fatal("two controls visible")
		}
		if it.Key == "pc_template" {
			n++
			if it.ValueJson != `"classic"` {
				t.Fatal(it.ValueJson)
			}
		}
	}
	if n != 1 {
		t.Fatal("single control missing")
	}
	cfg, e := NewStorefrontConfigService(r).GetPublicConfig(ctx, &emptypb.Empty{})
	if e != nil {
		t.Fatal(e)
	}
	for _, it := range cfg.Entries {
		if it.Key == "template.pc_template" || it.Key == "template.mobile_template" {
			if it.ValueJson != `"classic"` {
				t.Fatal("public config split")
			}
		}
	}
	// Old clients writing the mobile key still update the single active theme.
	if _, e = svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "template", Key: "mobile_template", ValueJson: `"classic"`}); e != nil {
		t.Fatal(e)
	}
	if string(r.values["template.mobile_template"]) != `"classic"` || string(r.values["template.pc_template"]) != `"classic"` {
		t.Fatal("legacy write not synchronized")
	}
	z := buildZip(t, map[string]string{"custom/theme.json": `{"name":"Custom","version":"1"}`, "custom/index.html": "<html><head></head><body>custom</body></html>"})
	if _, e = svc.InstallTemplate(ctx, &adminv1.InstallTemplateRequest{DataBase64: z}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{{Group: "template", Key: "pc_template", ValueJson: `"custom"`}}}); e != nil {
		t.Fatal(e)
	}
	if svc.ActiveTemplate(ctx) != "custom" || string(r.values["template.mobile_template"]) != `"custom"` {
		t.Fatal("custom theme not selected for both devices")
	}
	_, e = svc.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{{Group: "template", Key: "pc_template", ValueJson: `"classic"`}, {Group: "template", Key: "mobile_template", ValueJson: `"custom"`}}})
	if e == nil {
		t.Fatal("conflict accepted")
	}
	if svc.ActiveTemplate(ctx) != "custom" {
		t.Fatal("failed batch changed active theme")
	}
	if _, e = svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "template", Key: "pc_template", ValueJson: `"classic"`}); e != nil {
		t.Fatal("cannot switch back", e)
	}
}
