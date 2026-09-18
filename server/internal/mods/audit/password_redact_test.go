package audit

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChangePasswordCredentialsNeverEnterAudit(t *testing.T) {
	for _, payload := range []string{
		`{"current_password":"sensitive-old","new_password":"sensitive-new","confirm_password":"sensitive-confirm"}`,
		`{"currentPassword":"sensitive-old","newPassword":"sensitive-new","confirmPassword":"sensitive-confirm"}`,
	} {
		req := httptest.NewRequest("POST", "/api/v1/admin/auth/password", strings.NewReader(payload))
		raw, err := json.Marshal(ReadBodyJSON(req))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "sensitive-") {
			t.Fatal("password leaked into audit")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil || string(body) != payload {
			t.Fatal("request body was changed")
		}
	}
}
