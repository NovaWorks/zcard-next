package catalog

import (
	"bytes"
	"encoding/json"
	"google.golang.org/protobuf/types/known/structpb"
)

// Public catalogs preserve purchase rules without publishing the provider's cost.
// Purchases always use an authenticated total quote, never this display metadata.
func publicSocialSchema(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	b, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(b, &out) != nil {
		return nil
	}
	if billing, ok := out["billing"].(map[string]any); ok {
		delete(billing, "rate")
		billing["currency"] = "CNY"
		billing["mode"] = "quoted_total"
	}
	return out
}

func socialStruct(raw map[string]any) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	s, _ := structpb.NewStruct(publicSocialSchema(raw))
	return s
}

func sameSocialSchema(a, b map[string]any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}
