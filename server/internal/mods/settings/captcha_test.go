package settings

import (
	"context"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/go-kratos/kratos/v3/errors"
)

func TestCaptchaSettingsDefaults(t *testing.T) {
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	reply, err := svc.ListSettings(context.Background(), &adminv1.ListSettingsRequest{Group: "security"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*adminv1.Setting{}
	for _, item := range reply.Items {
		got[item.Key] = item
	}
	for key, want := range map[string]string{"captcha_length": "4", "captcha_charset": `"digit"`} {
		item := got[key]
		if item == nil || item.ValueJson != want {
			t.Fatalf("%s default: got %v, want %s", key, item, want)
		}
	}
	options := got["captcha_charset"].Options
	want := map[string]string{"digit": "纯数字", "letter": "纯字母", "mixed": "数字字母混合"}
	if len(options) != len(want) {
		t.Fatalf("charset options: %v", options)
	}
	for _, option := range options {
		if want[option.Value] != option.Label {
			t.Errorf("unexpected charset option %v", option)
		}
	}
}

func TestCaptchaSettingsRejectInvalidWrites(t *testing.T) {
	ctx := context.Background()
	for key, values := range map[string][]string{
		"captcha_length":  {`null`, `"6"`, `true`, `3`, `9`, `6.5`, `6.0`, `[]`, `{}`},
		"captcha_charset": {`null`, `6`, `true`, `""`, `"number"`, `"DIGIT"`, `[]`, `{}`},
	} {
		for _, value := range values {
			t.Run(key+"/"+value, func(t *testing.T) {
				repo := &themeMemoryRepo{values: map[string]json.RawMessage{}}
				svc := NewAdminSettingsService(NewSettingsUsecase(repo))
				_, err := svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "security", Key: key, ValueJson: value})
				if errors.FromError(err).GetReason() != "settings.INVALID_VALUE" {
					t.Fatalf("single write accepted %s=%s: %v", key, value, err)
				}
				_, err = svc.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{
					{Group: "site", Key: "name", ValueJson: `"changed"`},
					{Group: "security", Key: key, ValueJson: value},
				}})
				if errors.FromError(err).GetReason() != "settings.INVALID_VALUE" {
					t.Fatalf("batch write accepted %s=%s: %v", key, value, err)
				}
				if len(repo.values) != 0 {
					t.Fatalf("invalid write persisted values: %v", repo.values)
				}
			})
		}
	}
}

func TestCaptchaSettingsSaveAndRead(t *testing.T) {
	ctx := context.Background()
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	for _, value := range []string{"4", "5", "6", "7", "8"} {
		if _, err := svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "security", Key: "captcha_length", ValueJson: value}); err != nil {
			t.Fatalf("valid length %s rejected: %v", value, err)
		}
	}
	for _, charset := range []string{`"digit"`, `"letter"`, `"mixed"`} {
		_, err := svc.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{
			{Group: "security", Key: "captcha_length", ValueJson: "6"},
			{Group: "security", Key: "captcha_charset", ValueJson: charset},
		}})
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"captcha_length": "6", "captcha_charset": charset} {
			got, err := svc.GetSetting(ctx, &adminv1.GetSettingRequest{Group: "security", Key: key})
			if err != nil || got.ValueJson != want {
				t.Fatalf("%s read: got %v, want %s, err %v", key, got, want, err)
			}
		}
	}
}
