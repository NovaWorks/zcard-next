package port

import "testing"

func TestSocialLinePolicyAlsoControlsFieldBounds(t *testing.T) {
	min, max := int64(3), int64(3)
	s := SocialSchema{Version: 1, Fields: []SocialField{{Key: "comments", Type: "lines", Required: true, MinItems: &min, MaxItems: &max}}, Quantity: SocialQuantity{Mode: "derived_lines", LinesField: "comments", LinePolicy: "all", Min: &min, Max: &max}}
	in := SocialParameters{"comments": "first\r\n\r\nfirst"}
	out, n, err := ValidateSocialParameters(s, in)
	if err != nil || n == nil || *n != 3 || out["comments"] != in["comments"] {
		t.Fatalf("all-line policy diverged: %v %v", n, err)
	}
	s.Quantity.LinePolicy = "nonempty"
	if _, _, err = ValidateSocialParameters(s, in); err == nil {
		t.Fatal("nonempty policy ignored provider minimum")
	}
}

func TestSocialFieldBlankLineConstraint(t *testing.T) {
	min, max := int64(2), int64(5)
	schema := SocialSchema{Version: 1, Fields: []SocialField{{Key: "comments", Type: "lines", Required: true, MinItems: &min, MaxItems: &max, DisallowBlankLines: true}}, Quantity: SocialQuantity{Mode: "derived_lines", LinesField: "comments", LinePolicy: "all", Min: &min, Max: &max}}
	for _, value := range []string{"first\n\nsecond", "first\nsecond\n", "first\r\n \r\nsecond"} {
		if _, _, err := ValidateSocialParameters(schema, SocialParameters{"comments": value}); err == nil {
			t.Fatalf("blank comments accepted: %q", value)
		}
	}
	value := "same\r\nsame\n中文 & = +"
	out, quantity, err := ValidateSocialParameters(schema, SocialParameters{"comments": value})
	if err != nil || quantity == nil || *quantity != 3 || out["comments"] != value {
		t.Fatalf("valid comment contents changed: %v %v", quantity, err)
	}
	schema.Fields[0].DisallowBlankLines = false
	if _, _, err = ValidateSocialParameters(schema, SocialParameters{"comments": "first\n\nsecond"}); err != nil {
		t.Fatal("generic all-line services changed", err)
	}
}
