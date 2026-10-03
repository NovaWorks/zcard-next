package shipping

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCountryAddressValidation(t *testing.T) {
	base := map[string]string{"country": "US", "region": "CA", "city": "San Francisco", "address": "123 Test Street", "name": "Test Buyer", "phone": "+14155550100", "postal_code": "94105"}
	if _, e := Validate(base); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"region": "Ontario", "postal_code": "not-a-zip", "country": "XX", "name": "", "phone": "<script>", "address": "a\nb"} {
		a := map[string]string{}
		for key, value := range base {
			a[key] = value
		}
		a[k] = v
		if _, e := Validate(a); e == nil {
			t.Fatalf("invalid %s accepted", k)
		}
	}
	// A destination with no postal code system must remain orderable.
	base["country"] = "HK"
	base["region"] = "九龍"
	base["city"] = "Hong Kong"
	base["postal_code"] = ""
	if keys := Regions["HK"]["sub_keys"]; keys != "" {
		for i, c := range keys {
			if c == '~' {
				base["region"] = keys[:i]
				break
			}
		}
	}
	if _, e := Validate(base); e != nil {
		t.Fatal(e)
	}
}

func TestReviewPhoneRequiresDigits(t *testing.T) {
	a := map[string]string{"country": "US", "region": "CA", "city": "SF", "address": "Test Street", "name": "Buyer", "phone": "------", "postal_code": "94105"}
	if _, e := Validate(a); e == nil {
		t.Fatal("phone with no digits accepted")
	}
}

func TestAddressSecondLinePreservesLegacyFingerprint(t *testing.T) {
	legacy := map[string]string{"country": "AU", "region": "NSW", "city": "Sydney", "address": "3 Example Street", "district": "", "name": "Buyer", "phone": "+61412345678", "postal_code": "2000"}
	normalized, err := Normalize(legacy)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(normalized)
	legacy["address_line2"] = "  "
	normalized, err = Normalize(legacy)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(normalized)
	if string(got) != string(want) {
		t.Fatalf("empty optional address line changed persisted request fingerprint: %s != %s", got, want)
	}
	legacy["address_line2"] = "  Unit 4  "
	normalized, err = Validate(legacy)
	if err != nil || normalized["address_line2"] != "Unit 4" || normalized["address"] != "3 Example Street" || normalized["district"] != "" {
		t.Fatalf("second address line was lost or replaced district: %#v, %v", normalized, err)
	}
	for _, invalid := range []string{"Unit 4\nLevel 3", strings.Repeat("房", 301)} {
		legacy["address_line2"] = invalid
		if _, err := Validate(legacy); err == nil {
			t.Fatal("invalid optional address line accepted")
		}
	}
}

func TestCountrySpecificPostalAndOptionalState(t *testing.T) {
	base := map[string]string{"country": "GB", "city": "London", "address": "10 Example Street", "name": "Buyer", "phone": "+447123456789", "postal_code": "sw1a 1aa"}
	if _, err := Validate(base); err != nil {
		t.Fatal("case-insensitive British postcode rejected", err)
	}
	base["country"] = "AM"
	base["city"] = "Yerevan"
	base["postal_code"] = "0010"
	if _, err := Validate(base); err != nil {
		t.Fatal("country with optional state required a selection", err)
	}
	base["region"] = "Not a valid region"
	if _, err := Validate(base); err == nil {
		t.Fatal("invalid supplied optional state accepted")
	}
}
