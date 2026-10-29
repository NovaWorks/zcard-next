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
