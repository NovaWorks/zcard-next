package audit

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedContentNeverEntersAudit(t *testing.T) {
	body := `{"mode":"reuse","content":"account:secret","sku_id":2}`
	r := httptest.NewRequest("POST", "/api/v1/admin/products/7/delivery-sources", strings.NewReader(body))
	result := ReadBodyJSON(r)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "account:secret") || result["mode"] != "reuse" {
		t.Fatal("secret leaked or audit metadata removed")
	}
	raw, _ := io.ReadAll(r.Body)
	if string(raw) != body {
		t.Fatal("handler request body was corrupted")
	}
}
