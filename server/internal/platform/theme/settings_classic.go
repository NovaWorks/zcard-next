package theme

import (
	_ "embed"
	"encoding/json"
)

//go:embed classic.settings.json
var classicSettings []byte

func ClassicSettings() *SettingsSchema {
	var s SettingsSchema
	if err := json.Unmarshal(classicSettings, &s); err != nil {
		panic(err)
	}
	return &s
}
