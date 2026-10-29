package port

import (
	"reflect"
	"testing"
)

func socialTestInt(n int64) *int64 { return &n }
func testSocialSchema() SocialSchema {
	return SocialSchema{Version: 1, Revision: "v1", Fields: []SocialField{{Key: "link", Type: "url", Required: true}, {Key: "quantity", Type: "integer", Required: true}, {Key: "runs", Type: "integer", Min: socialTestInt(1)}}, Quantity: SocialQuantity{Mode: "explicit", Field: "quantity", Min: socialTestInt(1), Max: socialTestInt(10000000)}, Billing: SocialBilling{Mode: "quantity", Rate: "0.9", RateUnit: 1000, Currency: "USD", Verified: true}, Purchasable: true}
}

func TestSocialParametersPreserveOptionalAndLines(t *testing.T) {
	s := testSocialSchema()
	s.Fields = []SocialField{{Key: "link", Type: "url", Required: true}, {Key: "comments", Type: "lines", Required: true}, {Key: "posts", Type: "integer", Min: socialTestInt(0)}}
	s.Quantity = SocialQuantity{Mode: "derived_lines", LinesField: "comments", LinePolicy: "nonempty", Min: socialTestInt(2), Max: socialTestInt(10)}
	in := SocialParameters{"link": "https://example.com/post?q=a%20b", "comments": "重复\r\n重复\n\nthird\n", "posts": "0"}
	out, q, err := ValidateSocialParameters(s, in)
	if err != nil || q == nil || *q != 3 || !reflect.DeepEqual(in, out) {
		t.Fatalf("parameters changed or invalid quantity: %#v %v %v", out, q, err)
	}
	if _, ok := out["quantity"]; ok {
		t.Fatal("derived quantity must not become an unsupported provider parameter")
	}
	out["comments"] = "changed"
	if in["comments"] == "changed" {
		t.Fatal("validator returned input alias")
	}
	s.Quantity.LinePolicy = ""
	if _, _, err = ValidateSocialParameters(s, in); err == nil {
		t.Fatal("unverified line counting accepted")
	}
}

func TestSocialAbsentAndEventRange(t *testing.T) {
	s := testSocialSchema()
	s.Fields = []SocialField{{Key: "link", Type: "url", Required: true}}
	s.Quantity = SocialQuantity{Mode: "absent"}
	s.Billing.Mode = "fixed"
	p, q, err := ValidateSocialParameters(s, SocialParameters{"link": "https://example.com"})
	if err != nil || q != nil || len(p) != 1 {
		t.Fatalf("package fabricated quantity: %v %v", q, err)
	}
	s.Fields = []SocialField{{Key: "min", Type: "integer", Required: true}, {Key: "max", Type: "integer", Required: true}, {Key: "posts", Type: "integer", Min: socialTestInt(0)}, {Key: "expiry", Type: "date"}}
	s.Quantity = SocialQuantity{Mode: "range_per_event", MinField: "min", MaxField: "max", Min: socialTestInt(10), Max: socialTestInt(500)}
	s.Billing.Mode = "per_event"
	in := SocialParameters{"min": "10", "max": "500", "posts": "0", "expiry": "1/2/2028"}
	_, q, err = ValidateSocialParameters(s, in)
	if err != nil || q != nil {
		t.Fatalf("subscription range flattened: %v %v", q, err)
	}
	if _, err = SocialExactAmount(s, in); err == nil {
		t.Fatal("unbounded subscription got a fixed quote")
	}
	in["min"] = "501"
	if _, _, err = ValidateSocialParameters(s, in); err == nil {
		t.Fatal("out-of-range subscription accepted")
	}
}

func TestSocialFullRangeAndUnknownParameters(t *testing.T) {
	s := testSocialSchema()
	s.Quantity.Step = socialTestInt(7)
	s.Quantity.StepBase = socialTestInt(2)
	in := SocialParameters{"link": "https://example.com/", "quantity": "1000000"}
	if _, _, err := ValidateSocialParameters(s, in); err == nil {
		t.Fatal("invalid explicit step accepted")
	}
	in["quantity"] = "999994"
	if _, q, err := ValidateSocialParameters(s, in); err != nil || *q != 999994 {
		t.Fatalf("upstream range artificially limited: %v", err)
	}
	in["key"] = "secret"
	if _, _, err := ValidateSocialParameters(s, in); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestSocialWholeAmountAndScheduleBilling(t *testing.T) {
	s := testSocialSchema()
	in := SocialParameters{"link": "https://example.com", "quantity": "50"}
	exact, err := SocialExactAmount(s, in)
	if err != nil || exact != "0.045" {
		t.Fatalf("fractional rate lost: %s %v", exact, err)
	}
	amount, err := SocialAmountCents(s, in)
	if err != nil || amount != 5 {
		t.Fatalf("whole-total rounding: %d %v", amount, err)
	}
	in["runs"] = "3"
	if _, err = SocialExactAmount(s, in); err == nil {
		t.Fatal("unverified schedule billing accepted")
	}
	s.Billing.RunsMultiplierVerified = true
	exact, err = SocialExactAmount(s, in)
	if err != nil || exact != "0.135" {
		t.Fatalf("schedule total: %s %v", exact, err)
	}
	s.Billing.RateUnit = 3
	s.Billing.Rate = "1"
	in["quantity"] = "1"
	delete(in, "runs")
	if _, err = SocialExactAmount(s, in); err == nil {
		t.Fatal("non-terminating rate silently rounded")
	}
}

func TestSocialRevisionTracksConstraints(t *testing.T) {
	s := testSocialSchema()
	first := SocialSchemaRevision(s)
	s.Revision = "another"
	if SocialSchemaRevision(s) != first {
		t.Fatal("revision hashed itself")
	}
	s.Cancel = true
	if SocialSchemaRevision(s) == first {
		t.Fatal("purchase capability did not change revision")
	}
}
