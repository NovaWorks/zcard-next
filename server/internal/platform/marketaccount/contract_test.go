package marketaccount

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func examples(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b, e := os.ReadFile("testdata/examples.json")
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]json.RawMessage
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestWireExamples(t *testing.T) {
	all := examples(t)
	for _, v := range dtoTypes {
		n := reflect.TypeOf(v).Name()
		t.Run(n, func(t *testing.T) {
			raw, ok := all[n]
			if !ok {
				t.Fatal("missing frozen example")
			}
			out, _ := NewMessage(n)
			if e := Decode(n, raw, out, MaxResponseBytes); e != nil {
				t.Fatal(e)
			}
			encoded, e := json.Marshal(out)
			if e != nil {
				t.Fatal(e)
			}
			again, _ := NewMessage(n)
			if e = Decode(n, encoded, again, MaxResponseBytes); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(out, again) {
				t.Fatal("DTO roundtrip changes values")
			}
		})
	}
}
func TestRejectAmbiguousOrPrivilegedInput(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"Register", `{"requestId":"request-0000000001","password":"x","password":"y"}`},
		{"Empty", `null`}, {"Empty", `{} {}`}, {"Empty", `{"accountId":"71"}`},
		{"PageQuery", `{"limit":null}`}, {"PageQuery", `{"limit":1.0}`}, {"PageQuery", `{"limit":101}`}, {"PageQuery", `{"cursor":"a/b?"}`},
		{"ViewConfirm", `{"pairId":"` + strings.Repeat("b", 32) + `","deviceSecret":"` + strings.Repeat("a", 64) + `","accountId":71}`},
		{"ViewConfirm", `{"pairId":"` + strings.Repeat("b", 32) + `","deviceSecret":"` + strings.Repeat("a", 64) + `","accountId":"18446744073709551616"}`},
		{"ViewConfirm", `{"pairId":"` + strings.Repeat("b", 32) + `","deviceSecret":"` + strings.Repeat("a", 64) + `","accountId":"071"}`},
	} {
		out, _ := NewMessage(tc.name)
		if Decode(tc.name, []byte(tc.body), out, MaxRequestBytes) == nil {
			t.Errorf("accepted %s", tc.name)
		}
	}
	all := examples(t)
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"Register", "password", strings.Repeat("界", 25)},
		{"Register", "accountId", "72"}, {"SitePatch", "accountId", "72"}, {"SitePatch", "instanceId", "other-instance"},
		{"ViewStart", "scopes", []string{"purchase:write"}},
		{"Profile", "token", "secret"}, {"Entitlement", "signature", "secret"}, {"Order", "accountId", "72"},
		{"CodeRequest", "email", "BUYER@example.test"}, {"CodeRequest", "email", "Display <a@example.test>"},
		{"Wallet", "balance", "1.00"}, {"Wallet", "accountId", "72"}, {"Wallet", "currency", "USD"}, {"Order", "amount", "1.00"}, {"Price", "amount", "100000000000000000000"},
		{"ViewStatus", "customer", Customer{AccountID: "71"}},
	} {
		var m map[string]any
		json.Unmarshal(all[tc.name], &m)
		m[tc.key] = tc.value
		raw, _ := json.Marshal(m)
		out, _ := NewMessage(tc.name)
		if Decode(tc.name, raw, out, MaxResponseBytes) == nil {
			t.Errorf("accepted %s/%s", tc.name, tc.key)
		}
	}
	var dst Register
	if Decode("Register", all["Register"], &dst, 10) == nil {
		t.Fatal("body limit bypass")
	}
	if Decode("Empty", []byte("{\"x\":\"\xff\"}"), &Empty{}, 100) == nil {
		t.Fatal("invalid UTF8")
	}
}
func TestEndpointIdentityMatrix(t *testing.T) {
	seen := map[string]bool{}
	all := examples(t)
	rates := RateLimits()
	for _, e := range Endpoints() {
		key := e.Service + " " + e.Method + " " + e.Path
		if seen[key] {
			t.Fatal("duplicate endpoint", key)
		}
		seen[key] = true
		if e.Phase == "" || len(rates[e.RateClass]) == 0 {
			t.Fatal("unbounded endpoint", e.ID)
		}
		for _, n := range []string{e.Request, e.Response, e.PathSchema} {
			if n == "" {
				continue
			}
			out, err := NewMessage(n)
			if err != nil {
				t.Fatal(e.ID, err)
			}
			if err = Decode(n, all[n], out, e.MaxResponseBytes); err != nil {
				t.Fatal(e.ID, n, err)
			}
		}
		for _, identity := range []string{"instance_token", "legacy_user_jwt", "local_admin", "operator_admin", "account_view"} {
			if identity == "instance_token" || identity == "legacy_user_jwt" {
				if e.AcceptsIdentity(identity, e.Scope) {
					t.Fatal("credential confusion", e.ID)
				}
			}
			if identity == "account_view" && e.AcceptsIdentity(identity, e.Scope) {
				if (e.Method != "GET" || e.Scope == "") && e.ID != "view-revoke" {
					t.Fatal("read grant privilege", e.ID)
				}
			}
		}
		if e.AcceptsIdentity("customer_session", e.Scope) && e.Method != "GET" && !e.CSRF {
			t.Fatal("missing cookie CSRF", e.ID)
		}
		if e.AcceptsIdentity("account_view", e.Scope) && e.Scope != "" && e.AcceptsIdentity("account_view", "unrelated:read") {
			t.Fatal("scope bypass", e.ID)
		}
		if (e.AcceptsIdentity("local_admin", "") || e.AcceptsIdentity("operator_admin", "")) && e.Permission == "" {
			t.Fatal("missing RBAC", e.ID)
		}
	}
	if len(seen) != 49 {
		t.Fatalf("endpoint inventory changed: %d", len(seen))
	}
}
func TestProfileAndOriginBoundaries(t *testing.T) {
	for _, s := range []string{"http://market.example.test", "https://127.0.0.1", "https://market.example.test:443", "https://user@market.example.test", "https://MARKET.example.test", "https://market.example.test/", "https://market.example.test?q=x", "https://market.example.test#x", "https://market.example.test.", "https://[::1]"} {
		if ValidateOrigin(s) == nil {
			t.Errorf("accepted %s", s)
		}
	}
	var p PublicProfile
	if e := Decode("PublicProfile", examples(t)["PublicProfile"], &p, MaxResponseBytes); e != nil {
		t.Fatal(e)
	}
	if ValidateProfile(p, true) == nil {
		t.Fatal("fixture profile is publishable")
	}
	p.Origin = "https://plugins.vendor.tld"
	p.LicenseIssuer = p.Origin
	if e := ValidateProfile(p, true); e != nil {
		t.Fatal(e)
	}
	p.LicenseRoots = p.DistributionRoots
	if ValidateProfile(p, false) == nil {
		t.Fatal("reused signing key")
	}
}

func TestFrozenWire(t *testing.T) {
	raw, err := os.ReadFile("v1.sha256.json")
	if err != nil {
		t.Fatal(err)
	}
	var hashes map[string]string
	if err = json.Unmarshal(raw, &hashes); err != nil {
		t.Fatal(err)
	}
	for name, want := range hashes {
		raw, err = ContractFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(raw)
		if hex.EncodeToString(got[:]) != want {
			t.Fatal("unreviewed v1 contract change", name)
		}
	}
}
