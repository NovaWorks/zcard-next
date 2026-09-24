package audit

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramSettingsNeverEnterAudit(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/admin/settings/notify/telegram_bot_token", `{"value_json":"\"private-bot-token\""}`},
		{"/api/v1/admin/settings/notify/telegram", `{"valueJson":"{\"bot_token\":\"private-bot-token\"}"}`},
		{"/api/v1/admin/settings", `{"items":[{"group":"notify","key":"telegram_bot_token","value_json":"\"private-bot-token\""}]}`},
	} {
		req := httptest.NewRequest("PUT", tc.path, strings.NewReader(tc.body))
		b, _ := json.Marshal(ReadBodyJSON(req))
		if strings.Contains(string(b), "private-bot-token") {
			t.Fatal("audit leaked token", string(b))
		}
		raw, _ := io.ReadAll(req.Body)
		if string(raw) != tc.body {
			t.Fatal("request body changed")
		}
	}
}
