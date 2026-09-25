package settings

import (
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
)

// Show legacy values in the new form without moving the encrypted token's key.
func telegramLegacyDefaults(items []port.Item) []port.Item {
	var legacy json.RawMessage
	have := map[string]bool{}
	for _, it := range items {
		if it.Group != "notify" {
			continue
		}
		have[it.Key] = true
		if it.Key == "telegram_enabled" {
			return items
		}
		if it.Key == "telegram" {
			legacy = it.Value
		}
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(legacy, &values) != nil {
		return items
	}
	for _, key := range []string{"enabled", "order_enabled", "bot_token", "chat_ids", "events"} {
		if value, ok := values[key]; ok && !have["telegram_"+key] {
			items = append(items, port.Item{Group: "notify", Key: "telegram_" + key, Value: value})
		}
	}
	return items
}
