package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInventoryAuditKeepsMetadataAndRestoresPayload(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/inventory/preview", "/api/v1/admin/inventory/import", "/api/v1/admin/inventory/assign-preview", "/api/v1/admin/inventory/assign"} {
		t.Run(path, func(t *testing.T) {
			body := `{"productId":7,"sku_id":3,"dedup":true,"Lines":["CREDENTIAL-ONE","CREDENTIAL-TWO"],"sample":["SAMPLE-SECRET"],"content":"EXTRA-SECRET","card_ids":[2,4],"request_key":"REQUEST-SECRET","requestKey":"CAMEL-SECRET"}`
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			metadata := ReadBodyJSON(r)
			encoded, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"CREDENTIAL-ONE", "CREDENTIAL-TWO", "SAMPLE-SECRET", "EXTRA-SECRET", "REQUEST-SECRET", "CAMEL-SECRET"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("inventory credential remained in audit metadata")
				}
			}
			if metadata["productId"] != float64(7) || metadata["sku_id"] != float64(3) || metadata["line_count"] != 2 {
				t.Fatalf("necessary audit metadata lost: %v", metadata)
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil || string(raw) != body {
				t.Fatal("redaction changed the actual handler payload")
			}
		})
	}
}

func TestInventoryAuditDropsArbitraryValuesInMetadataAliases(t *testing.T) {
	body := `{"product_id":"7","sku_id":"3","ProductId":"ALIAS-SECRET","skuId":{"lines":["NESTED-SECRET"]},"cardIds":[2,"CARD-SECRET"],"expected_revision":"REVISION-SECRET","enable":"ENABLE-SECRET","dedup":true}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/inventory/assign", strings.NewReader(body))
	metadata := ReadBodyJSON(r)
	raw, err := json.Marshal(metadata)
	if err != nil || strings.Contains(string(raw), "SECRET") {
		t.Fatalf("unvalidated inventory metadata retained arbitrary plaintext: %s %v", raw, err)
	}
	if metadata["product_id"] != "7" || metadata["sku_id"] != "3" || metadata["dedup"] != true {
		t.Fatalf("protobuf decimal-string IDs were lost: %s", raw)
	}
}

func TestInventoryHTTPAuditNeverPersistsImportLines(t *testing.T) {
	repo, d := newAuditRepo(t)
	ctx := context.Background()
	const body = `{"product_id":9,"skuId":6,"lines":["HTTP-IMPORT-SECRET"],"sample":["HTTP-SAMPLE-SECRET"]}`
	received := make(chan string, 2)
	filter := OpAuditFilter(repo, nil, func(string) (string, bool) { return "inventory:import", true }, func(*http.Request) string { return "inventory-import" })
	server := httptest.NewServer(filter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received <- string(raw)
		w.WriteHeader(http.StatusNoContent)
	})))
	defer server.Close()
	for _, path := range []string{"/api/v1/admin/inventory/preview", "/api/v1/admin/inventory/import"} {
		response, err := server.Client().Post(server.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent || <-received != body {
			t.Fatal("HTTP import did not receive the original payload")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows, err := d.Client.AuditLog.Query().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 2 {
			for _, row := range rows {
				encoded, err := json.Marshal(row.After)
				if err != nil || strings.Contains(string(encoded), "SECRET") {
					t.Fatal("plaintext inventory data was persisted to audit_logs")
				}
				if row.After["product_id"] != float64(9) || row.After["skuId"] != float64(6) || row.After["line_count"] != float64(1) {
					t.Fatalf("persisted audit lost target/count: %s", encoded)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit writes did not finish: got %d rows", len(rows))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
