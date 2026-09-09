package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/protobuf/types/known/emptypb"
	"testing"
)

func TestPublicAffiliateLevels(t *testing.T) {
	for levels := 1; levels <= 3; levels++ {
		repo := &themeMemoryRepo{values: map[string]json.RawMessage{
			"affiliate.enabled": json.RawMessage(`true`),
			"affiliate.levels":  json.RawMessage(fmt.Sprint(levels)),
			"affiliate.base":    json.RawMessage(`"profit"`),
			"affiliate.rate_l1": json.RawMessage(`20`),
		}}
		cfg, err := NewStorefrontConfigService(repo).GetPublicConfig(context.Background(), &emptypb.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for _, e := range cfg.Entries {
			values[e.Key] = e.ValueJson
		}
		if values["affiliate.levels"] != fmt.Sprint(levels) || values["affiliate.base"] != `"profit"` || values["affiliate.enabled"] != "true" {
			t.Fatalf("affiliate config mismatch: %v", values)
		}
		if _, ok := values["affiliate.rate_l1"]; ok {
			t.Fatal("unexpected nonpublic affiliate setting")
		}
	}
}
