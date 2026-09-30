package audit

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSMTPSettingsNeverEnterAudit(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/admin/settings/notify/smtp_password", `{"value_json":"\"smtp-secret\""}`},
		{"/api/v1/admin/settings", `{"items":[{"group":"notify","key":"smtp_password","value_json":"\"smtp-secret\""}]}`},
	} {
		r := httptest.NewRequest("PUT", tc.path, strings.NewReader(tc.body))
		raw, _ := json.Marshal(ReadBodyJSON(r))
		if strings.Contains(string(raw), "smtp-secret") {
			t.Fatal("SMTP password leaked to audit", string(raw))
		}
	}
}
