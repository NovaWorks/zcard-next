package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
)

func TestSMTPSettingValidationAndSecretDefaults(t *testing.T) {
	svc := NewAdminSettingsService(nil)
	for _, tc := range []struct {
		key, raw string
		valid    bool
	}{
		{"smtp_security", `"auto"`, true}, {"smtp_security", `"plain"`, true}, {"smtp_security", `"tls"`, true}, {"smtp_security", `"starttls"`, true},
		{"smtp_security", `"dkim"`, false}, {"smtp_security", `null`, false},
		{"smtp_auth", `"login"`, true}, {"smtp_auth", `"cram_md5"`, true}, {"smtp_auth", `"none"`, true}, {"smtp_auth", `"invalid"`, false},
		{"smtp_tls_verify", `true`, true}, {"smtp_tls_verify", `false`, true}, {"smtp_tls_verify", `null`, false}, {"smtp_tls_verify", `"false"`, false},
		{"smtp_port", `465`, true}, {"smtp_port", `2525`, true}, {"smtp_port", `0`, false}, {"smtp_port", `65536`, false}, {"smtp_port", `465.5`, false},
	} {
		err := svc.validateSettingValue(context.Background(), "notify", tc.key, json.RawMessage(tc.raw))
		if (err == nil) != tc.valid {
			t.Errorf("%s %s valid=%v: %v", tc.key, tc.raw, tc.valid, err)
		}
	}
	items := SanitizeGroup(withDefaults("notify", []port.Item{{Group: "notify", Key: "smtp", Value: json.RawMessage(`{"password":"legacy-secret"}`)}}))
	values := map[string]string{}
	for _, it := range items {
		values[it.Key] = string(it.Value)
	}
	if values["smtp"] != `"****"` || values["smtp_tls_verify"] != "true" || values["smtp_security"] != `"auto"` {
		t.Fatal("unsafe legacy/default SMTP config", values)
	}
}
