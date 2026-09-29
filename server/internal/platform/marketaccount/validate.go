package marketaccount

import (
	"bytes"
	"crypto/ed25519"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	pl "github.com/NovaWorks/zcard-next/server/internal/platform/pluginlicense"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/mod/semver"
)

// ContractFiles contains protocol source, not live authorization configuration.
//
//go:embed wire.schema.json routes.json rates.json errors.json
var ContractFiles embed.FS

var ErrInvalid = errors.New("market account: invalid protocol document")
var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var once sync.Once
var schemas map[string]*jsonschema.Schema
var compileErr error

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading forbidden")
}

var dtoTypes = []any{
	HostContext{}, HostBegin{}, HostConfirm{}, HostPage{}, CustomerPath{}, CustomerStatus{}, CustomerRecord{}, CustomerPage{},
	Empty{}, Operation{}, PageQuery{}, ProductQuery{}, PluginPath{}, SitePath{}, Bootstrap{},
	CodeRequest{}, CodeAccepted{}, Register{}, ResetPassword{}, VerifyEmail{}, Login{}, Customer{},
	Session{}, Profile{}, Wallet{}, ListingQuery{}, Release{}, Listing{}, ListingPage{}, Facets{}, Accepted{}, ViewStart{}, ViewPair{}, ViewProof{}, ViewConfirm{}, ViewApproval{},
	ViewLookup{}, ViewTarget{}, ViewStatus{}, ViewCredential{}, SitePatch{}, SiteOperation{}, Site{},
	SitePage{}, SiteChallenge{}, Verification{}, Order{}, OrderPage{}, Entitlement{}, EntitlementPage{},
	Version{}, Price{}, Product{}, ProductPage{}, Capabilities{}, PublicProfile{}, Failure{},
}

// NewMessage returns a wire DTO. Call Decode before using untrusted values.
func NewMessage(name string) (any, error) {
	for _, v := range dtoTypes {
		t := reflect.TypeOf(v)
		if t.Name() == name {
			return reflect.New(t).Interface(), nil
		}
	}
	return nil, ErrInvalid
}
func initSchemas() {
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	c.AssertFormat()
	for _, name := range []string{"uint64-positive", "int64-positive", "cny-cents", "market-email", "new-password", "login-password", "market-origin", "ed25519-public", "semver"} {
		n := name
		c.RegisterFormat(&jsonschema.Format{Name: n, Validate: func(v any) error { return checkFormat(n, v) }})
	}
	raw, _ := ContractFiles.ReadFile("wire.schema.json")
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		compileErr = err
		return
	}
	const base = "https://zcard.invalid/market/account/v1"
	if err = c.AddResource(base, value); err != nil {
		compileErr = err
		return
	}
	schemas = map[string]*jsonschema.Schema{}
	for _, v := range dtoTypes {
		n := reflect.TypeOf(v).Name()
		s, e := c.Compile(base + "#/$defs/" + n)
		if e != nil {
			compileErr = e
			return
		}
		schemas[n] = s
	}
}

// Decode rejects duplicate/unknown fields, nulls, unsafe decimals and invalid
// UTF-8. Validation errors deliberately omit input values (passwords/tokens).
// limit is selected from the endpoint contract, never from remote input.
func Decode(name string, raw []byte, out any, limit int) error {
	once.Do(initSchemas)
	if compileErr != nil {
		return fmt.Errorf("embedded account schema: %w", compileErr)
	}
	schema, ok := schemas[name]
	if !ok {
		return ErrInvalid
	}
	dto, err := NewMessage(name)
	if err != nil {
		return err
	}
	if reflect.TypeOf(out) != reflect.TypeOf(dto) || reflect.ValueOf(out).IsNil() {
		return ErrInvalid
	}
	if err = pl.DecodeLimit(raw, dto, limit); err != nil {
		return ErrInvalid
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil || schema.Validate(v) != nil {
		return ErrInvalid
	}
	// Cross-field checks that cannot be expressed with the fixed JSON schema.
	switch x := dto.(type) {
	case *PublicProfile:
		if ValidateProfile(*x, false) != nil {
			return ErrInvalid
		}
	case *Version:
		if semver.Compare("v"+x.MinCore, "v"+x.MaxCoreExclusive) >= 0 {
			return ErrInvalid
		}
	case *Entitlement:
		if x.ExpiresAt <= x.NotBefore {
			return ErrInvalid
		}
		if d, e := pl.Domain(x.Domain); e != nil || d != x.Domain {
			return ErrInvalid
		}
		if x.MinVersion != "" && x.MaxVersionExclusive != "" && semver.Compare("v"+x.MinVersion, "v"+x.MaxVersionExclusive) >= 0 {
			return ErrInvalid
		}
	case *Site:
		if x.VerificationState == "verified" && (x.VerifiedOrigin == "" || x.SiteURL != x.VerifiedOrigin || x.VerifiedAt == 0) {
			return ErrInvalid
		}
	}
	// Recursively check the same semantics when DTOs occur in a page or product.
	if err = checkChildren(dto); err != nil {
		return err
	}
	reflect.ValueOf(out).Elem().Set(reflect.ValueOf(dto).Elem())
	return nil
}
func checkChildren(dto any) error {
	check := func(name string, v any) error {
		b, e := json.Marshal(v)
		if e != nil {
			return ErrInvalid
		}
		out, _ := NewMessage(name)
		return Decode(name, b, out, MaxResponseBytes)
	}
	switch x := dto.(type) {
	case *SitePage:
		for _, v := range x.Items {
			if e := check("Site", v); e != nil {
				return e
			}
		}
	case *EntitlementPage:
		for _, v := range x.Items {
			if e := check("Entitlement", v); e != nil {
				return e
			}
		}
	case *ProductPage:
		for _, v := range x.Items {
			if e := check("Product", v); e != nil {
				return e
			}
		}
	case *Product:
		for _, v := range x.Versions {
			if e := check("Version", v); e != nil {
				return e
			}
		}
		if x.Sellable && x.Price == nil {
			return ErrInvalid
		}
	}
	return nil
}
func checkFormat(name string, v any) error {
	s, ok := v.(string)
	if !ok {
		return ErrInvalid
	}
	switch name {
	case "uint64-positive":
		n, e := strconv.ParseUint(s, 10, 64)
		if e != nil || n == 0 || strconv.FormatUint(n, 10) != s {
			return ErrInvalid
		}
	case "int64-positive":
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
			return ErrInvalid
		}
	case "cny-cents":
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || !money.ValidCents(n) || strconv.FormatInt(n, 10) != s {
			return ErrInvalid
		}
	case "market-email":
		a, e := mail.ParseAddress(s)
		if e != nil || a.Name != "" || a.Address != s || s != strings.ToLower(strings.TrimSpace(s)) || len(s) > 255 {
			return ErrInvalid
		}
		parts := strings.Split(s, "@")
		if len(parts) != 2 || !strings.Contains(parts[1], ".") {
			return ErrInvalid
		}
		for _, r := range s {
			if r > 127 || r <= 32 {
				return ErrInvalid
			}
		}
	case "new-password":
		if len(s) < 8 || len(s) > 72 || !utf8.ValidString(s) {
			return ErrInvalid
		}
	case "login-password":
		if len(s) < 1 || len(s) > 72 || !utf8.ValidString(s) {
			return ErrInvalid
		}
	case "market-origin":
		return ValidateOrigin(s)
	case "ed25519-public":
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil || len(b) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(b) != s {
			return ErrInvalid
		}
	case "semver":
		if !semver.IsValid("v" + s) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// ValidateOrigin checks syntax only. It does not replace DNS/dial-time SSRF
// enforcement by httpx. Canonical production origins omit the default port.
func ValidateOrigin(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || !pl.ValidIssuer(raw) || u.Port() != "" || net.ParseIP(u.Hostname()) != nil || !strings.Contains(u.Hostname(), ".") {
		return ErrInvalid
	}
	d, e := pl.Domain(u.Hostname())
	if e != nil || d != u.Hostname() {
		return ErrInvalid
	}
	return nil
}

// ValidateProfile is for a locally provisioned public profile, never TOFU.
// official rejects reserved fixture domains; DNS safety still belongs to httpx.
func ValidateProfile(p PublicProfile, official bool) error {
	if !identifier.MatchString(p.ProfileID) || ValidateOrigin(p.Origin) != nil || p.LicenseIssuer != p.Origin || len(p.DistributionRoots) == 0 || len(p.LicenseRoots) == 0 || len(p.DistributionRoots) > 32 || len(p.LicenseRoots) > 32 {
		return ErrInvalid
	}
	u, _ := url.Parse(p.Origin)
	host := u.Hostname()
	if official {
		for _, s := range []string{"test", "invalid", "example", "localhost", "local", "example.com", "example.net", "example.org"} {
			if host == s || strings.HasSuffix(host, "."+s) {
				return ErrInvalid
			}
		}
	}
	dist := map[string]bool{}
	for _, roots := range []map[string]string{p.DistributionRoots, p.LicenseRoots} {
		for keyID, key := range roots {
			if !identifier.MatchString(keyID) {
				return ErrInvalid
			}
			if checkFormat("ed25519-public", key) != nil {
				return ErrInvalid
			}
		}
	}
	for _, key := range p.DistributionRoots {
		dist[key] = true
	}
	for _, key := range p.LicenseRoots {
		if dist[key] {
			return ErrInvalid
		}
	}
	return nil
}
