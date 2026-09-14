package audit

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMFACredentialsNeverEnterAudit(t *testing.T) {
	payload := `{"password":"test-password-value","totp_code":"123456","challenge":"sensitive-challenge","nested":{"recovery_ticket":"sensitive-ticket"},"items":[{"recovery_codes":["sensitive-recovery"]}],"secret":"sensitive-secret"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/auth/totp/confirm", strings.NewReader(payload))
	raw, err := json.Marshal(ReadBodyJSON(req))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"test-password-value", "123456", "sensitive-challenge", "sensitive-ticket", "sensitive-recovery", "sensitive-secret"} {
		if strings.Contains(string(raw), s) {
			t.Fatal("credential leaked into audit")
		}
	}
}
