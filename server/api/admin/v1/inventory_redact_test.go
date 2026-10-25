package adminv1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v3/middleware/logging"
	"google.golang.org/protobuf/proto"
)

func TestInventoryRequestsRedactKratosServerLogs(t *testing.T) {
	revision := int64(8)
	for _, tc := range []struct {
		name string
		req  proto.Message
		want string
	}{
		{"preview", &ImportPreviewRequest{ProductId: 7, SkuId: 3, Lines: []string{"IMPORT-SECRET", "SECOND-SECRET"}, Dedup: true}, "line_count:2 dedup:true"},
		{"confirm", &ImportConfirmRequest{ProductId: 7, SkuId: 3, Lines: []string{"IMPORT-SECRET"}, ExpectedRevision: &revision}, "expected_revision:8"},
		{"assign", &AssignCardsRequest{ProductId: 7, SkuId: 3, CardIds: []uint64{11, 12}, RequestKey: "REQUEST-SECRET", ExpectedRevision: &revision}, "card_ids:[11 12] expected_revision:8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			original := proto.Clone(tc.req)
			handler := logging.Server(logger)(func(_ context.Context, req any) (any, error) {
				if req != tc.req || !proto.Equal(req.(proto.Message), original) {
					t.Fatal("logging changed the handler request")
				}
				return nil, nil
			})
			if _, err := handler(context.Background(), tc.req); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "SECRET") {
				t.Fatal("plaintext inventory data reached Kratos request logs")
			}
			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			args, ok := entry["args"].(string)
			if !ok || !strings.Contains(args, "product_id:7 sku_id:3") || !strings.Contains(args, tc.want) {
				t.Fatalf("missing request metadata in server log: %s", output.String())
			}
		})
	}
}

func TestInventoryRedactNilRequests(t *testing.T) {
	for _, req := range []logging.Redacter{(*ImportPreviewRequest)(nil), (*ImportConfirmRequest)(nil), (*AssignCardsRequest)(nil)} {
		if req.Redact() == "" {
			t.Fatal("nil request omitted logging metadata")
		}
	}
}
