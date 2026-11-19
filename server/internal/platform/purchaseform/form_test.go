package purchaseform

import (
	"encoding/json"
	"testing"
)

func TestSupplierFieldTypesAndRules(t *testing.T) {
	fields, err := ParseSchema(json.RawMessage(`{"fields":[
		{"key":"account","type":"text","label":{"zh-CN":"账号"},"required":true,"regex":"/^[a-z]+$/i","max_len":6},
		{"key":"count","type":"number","min":1,"max":3},
		{"key":"services","type":"checkbox","options":["A","B"]},
		{"key":"email","type":"email"}, {"key":"phone","type":"phone"}]}`))
	if err != nil || len(fields) != 5 {
		t.Fatalf("schema: %v", err)
	}
	for _, tc := range []struct {
		field int
		value string
		valid bool
	}{{0, "Abc", true}, {0, "", false}, {0, "toolong", false}, {0, "abc1", false}, {1, "2", true}, {1, "4", false}, {1, "NaN", false}, {2, "A,B", true}, {2, "A,A", false}, {2, "C", false}, {3, "a@example.test", true}, {3, "bad", false}, {4, "+12345678", true}, {4, "bad", false}} {
		if _, err := fields[tc.field].Value(tc.value); (err == nil) != tc.valid {
			t.Errorf("field %d valid=%v: %v", tc.field, tc.valid, err)
		}
	}
	value, _ := fields[1].Value("2")
	if _, ok := value.(float64); !ok {
		t.Fatal("numbers must not be forwarded as display strings")
	}
	value, _ = fields[2].Value("A,B")
	if len(value.([]string)) != 2 {
		t.Fatal("checkbox values must remain arrays")
	}
	for _, raw := range []string{`{"fields":[{"key":"x","type":"unknown"}]}`, `{"fields":[{"key":"x","type":"text"},{"key":"x","type":"text"}]}`, `{"fields":[{"key":"x","type":"text","regex":"["}]}`} {
		if _, err := ParseSchema(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted malformed supplier schema")
		}
	}
}
