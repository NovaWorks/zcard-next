package settings

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/types/known/emptypb"
	"testing"
)

func TestPublicCartToggle(t *testing.T) {
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{}}
	svc := NewStorefrontConfigService(repo)
	for _, value := range []string{"", "false", "true"} {
		if value != "" {
			repo.values["trade.cart_enabled"] = json.RawMessage(value)
		}
		cfg, err := svc.GetPublicConfig(context.Background(), &emptypb.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		expected := value
		if expected == "" {
			expected = "true"
		}
		found := false
		for _, entry := range cfg.Entries {
			if entry.Key == "trade.cart_enabled" {
				found = true
				if entry.ValueJson != expected {
					t.Fatalf("cart toggle=%s, want %s", entry.ValueJson, expected)
				}
			}
		}
		if !found {
			t.Fatal("cart toggle missing from public config")
		}
	}
}
