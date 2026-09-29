package shipping

import "testing"

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
