package orderaccess

import "testing"

func TestOrderTokenScope(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	hash := TokenHash(2, "ORDER", token)
	if !VerifyToken(hash, 2, "ORDER", token) {
		t.Fatal("issued credential was rejected")
	}
	other, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tenant    uint64
		no, value string
	}{
		{3, "ORDER", token}, {2, "OTHER", token}, {2, "ORDER", other},
		{2, "ORDER", ""}, {2, "ORDER", "invalid"},
	} {
		if VerifyToken(hash, tc.tenant, tc.no, tc.value) {
			t.Fatalf("credential accepted outside its scope: %v", tc)
		}
	}
	if VerifyToken("", 2, "ORDER", token) {
		t.Fatal("unprotected order accepted a credential")
	}
}
