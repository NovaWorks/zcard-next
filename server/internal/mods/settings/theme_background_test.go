package settings

import (
	"context"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestThemeBackgroundInheritsLegacyAndPublishesRemoval(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{
		"template.bg_image":        json.RawMessage(`"/uploads/desktop.png"`),
		"template.bg_image_mobile": json.RawMessage(`"/uploads/mobile.png"`),
	}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	reply, err := svc.GetThemeSettings(ctx, &adminv1.ThemeSettingsRequest{Key: "classic"})
	if err != nil {
		t.Fatal(err)
	}
	var initial struct {
		Values map[string]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(reply.StateJson), &initial); err != nil {
		t.Fatal(err)
	}
	values := initial.Values
	for key, expected := range map[string]string{"template.bg_image": "/uploads/desktop.png", "template.bg_image_mobile": "/uploads/mobile.png"} {
		if values[key] != expected {
			t.Fatalf("first customization lost legacy %s: %v", key, values[key])
		}
	}
	save := func(action string) {
		t.Helper()
		state, err := readThemeState(ctx, repo, "classic")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.SaveThemeSettings(ctx, &adminv1.SaveThemeSettingsRequest{
			Key: "classic", ThemeRevision: "builtin", ExpectedRevision: state.Revision,
			Action: action, ValuesJson: string(raw),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	publicBackgrounds := func(desktop, mobile string) {
		t.Helper()
		// New service instance: observe persisted configuration, without any browser cache.
		public, err := NewStorefrontConfigService(repo).GetPublicConfig(ctx, &emptypb.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, entry := range public.Entries {
			if entry.Key == "template.bg_image" || entry.Key == "template.bg_image_mobile" {
				var value string
				if err := json.Unmarshal([]byte(entry.ValueJson), &value); err != nil {
					t.Fatal(err)
				}
				found[entry.Key] = value
			}
		}
		if len(found) != 2 || found["template.bg_image"] != desktop || found["template.bg_image_mobile"] != mobile {
			t.Fatalf("unexpected public backgrounds: %#v", found)
		}
	}
	publicBackgrounds("/uploads/desktop.png", "/uploads/mobile.png")
	values["theme.brand_bar_enabled"] = false
	save("publish")
	// Reproduce the old duplicate-editor issue: global values clear successfully,
	// but the separately published theme snapshot still supplies both images.
	_, err = svc.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{
		{Group: "template", Key: "bg_image", ValueJson: `""`},
		{Group: "template", Key: "bg_image_mobile", ValueJson: `""`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	publicBackgrounds("/uploads/desktop.png", "/uploads/mobile.png")
	// Keep nonempty legacy values to prove an explicit theme deletion never falls back.
	repo.values["template.bg_image"] = json.RawMessage(`"/uploads/desktop.png"`)
	repo.values["template.bg_image_mobile"] = json.RawMessage(`"/uploads/mobile.png"`)
	values["template.bg_image"] = ""
	values["template.bg_image_mobile"] = ""
	save("draft")
	publicBackgrounds("/uploads/desktop.png", "/uploads/mobile.png")
	save("publish")
	publicBackgrounds("", "")
	runtime, err := svc.runtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Values["theme.brand_bar_enabled"] != false {
		t.Fatal("background deletion changed another theme setting")
	}
	if string(repo.values["template.bg_image"]) != `"/uploads/desktop.png"` {
		t.Fatal("legacy compatibility data overwritten")
	}
	t.Log("legacy values inherited; duplicate global delete reproduced; draft isolated; published empty backgrounds override old values")
}
