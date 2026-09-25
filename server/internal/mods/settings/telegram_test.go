package settings

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"testing"
)

func TestTelegramDestinationValidation(t *testing.T) {
	s := NewAdminSettingsService(nil)
	for _, raw := range []string{`null`, `{}`, `[{"chat_id":"0"}]`, `[{"chat_id":"-100","topic_id":-1}]`, `[{"chat_id":"-100","topic_id":1.2}]`, `[{"chat_id":"-100","topic_id":2147483648}]`, `[{"chat_id":"@Abcde","topic_id":1},{"chat_id":"@abcde","topic_id":1}]`, `[{"chat_id":"11"},{"chat_id":"011"}]`} {
		if e := s.validateSettingValue(context.Background(), "notify", "telegram_targets", json.RawMessage(raw)); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`[]`, `[{"chat_id":"-100"}]`, `[{"chat_id":"-100","topic_id":1},{"chat_id":"-100","topic_id":2}]`} {
		if e := s.validateSettingValue(context.Background(), "notify", "telegram_targets", json.RawMessage(raw)); e != nil {
			t.Fatal(raw, e)
		}
	}
}
func TestTelegramLegacyFormDoesNotExposeToken(t *testing.T) {
	items := []port.Item{{Group: "notify", Key: "telegram", Value: json.RawMessage(`{"enabled":true,"bot_token":"123:legacy","chat_ids":"11"}`)}}
	items = SanitizeGroup(withDefaults("notify", telegramLegacyDefaults(items)))
	got := map[string]string{}
	for _, it := range items {
		got[it.Key] = string(it.Value)
	}
	if got["telegram_bot_token"] != `"****"` || got["telegram_chat_ids"] != `"11"` || got["telegram_enabled"] != "true" {
		t.Fatal("legacy form defaults", got)
	}
	items = SanitizeGroup(withDefaults("notify", nil))
	for _, it := range items {
		if it.Key == "telegram_bot_token" && string(it.Value) != `""` {
			t.Fatal("unconfigured token shown configured")
		}
	}
}
