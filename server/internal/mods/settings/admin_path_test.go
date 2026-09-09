package settings

import (
	"context"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestAdminPathSettings(t *testing.T) {
	ctx := context.Background()
	r := &themeMemoryRepo{values: map[string]json.RawMessage{}}
	s := NewAdminSettingsService(NewSettingsUsecase(r))
	if err := s.SetAdminBasePath("/startup"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AdminPath(ctx); err != nil || got != "/startup" {
		t.Fatal(got, err)
	}
	reply, err := s.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{{Group: "site", Key: "admin_path", ValueJson: `"/private-72/"`}}})
	if err != nil || reply.AdminBasePath != "/private-72" {
		t.Fatal(reply, err)
	}
	if got, err := s.AdminPath(ctx); err != nil || got != "/private-72" {
		t.Fatal(got, err)
	}
	for _, invalid := range []string{`"/api"`, `"/"`, `"has space"`, `null`, `true`, `123`} {
		_, err = s.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{{Group: "site", Key: "name", ValueJson: `"changed"`}, {Group: "site", Key: "admin_path", ValueJson: invalid}}})
		if err == nil {
			t.Fatal("invalid path accepted", invalid)
		}
		if _, exists := r.values["site.name"]; exists {
			t.Fatal("invalid batch partially saved")
		}
	}
	cfg, err := NewStorefrontConfigService(r).GetPublicConfig(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cfg.Entries {
		if e.Key == "site.admin_path" {
			t.Fatal("private entry exposed in public config")
		}
	}
	_, err = s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "site", Key: "admin_path", ValueJson: `"/next"`})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AdminPath(ctx); got != "/next" {
		t.Fatal(got)
	}
	reply, err = s.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{{Group: "site", Key: "admin_path", ValueJson: `""`}}})
	if err != nil || reply.AdminBasePath != "/startup" {
		t.Fatal(reply, err)
	}
	r.values["site.admin_path"] = json.RawMessage(`"/api"`)
	if _, err = s.AdminPath(ctx); err == nil {
		t.Fatal("invalid persisted path reopened fallback")
	}
}
