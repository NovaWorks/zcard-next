package plugincontract

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/mod/semver"
)

//go:embed schema/*.json
var schemas embed.FS
var compiled map[Kind]*jsonschema.Schema
var compileErr error
var compileOnce sync.Once

type offlineLoader struct{}

func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema forbidden: %s", url)
}

func compileSchemas() {
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	compiled = make(map[Kind]*jsonschema.Schema)
	for _, kind := range []Kind{ManifestKind, ConfigKind, InputKind, DecisionKind, ArtifactKind} {
		b, err := schemas.ReadFile("schema/" + string(kind) + ".json")
		if err != nil {
			compileErr = err
			return
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			compileErr = err
			return
		}
		url := "https://zcard.invalid/plugin/v1/" + string(kind) + ".json"
		if err = c.AddResource(url, v); err != nil {
			compileErr = err
			return
		}
		s, err := c.Compile(url)
		if err != nil {
			compileErr = err
			return
		}
		compiled[kind] = s
	}
}

// Validate applies the embedded JSON schema and cross-field semantics. It never
// fetches schema URLs or trusts a document's own $schema. Do not bypass it by
// directly decoding untrusted data into the exported DTOs.
func Validate(kind Kind, raw []byte) error {
	compileOnce.Do(compileSchemas)
	if compileErr != nil {
		return fmt.Errorf("compile embedded plugin schema: %w", compileErr)
	}
	s, ok := compiled[kind]
	if !ok {
		return invalid("unknown document kind")
	}
	if len(raw) > MaxJSONBytes || !utf8.Valid(raw) {
		return invalid("JSON exceeds size limit or is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	budget := 16384
	v, err := readValue(d, 0, &budget)
	if err != nil {
		return invalid(err.Error())
	}
	if _, err = d.Token(); err != io.EOF {
		return invalid("trailing JSON or malformed document")
	}
	if err = s.Validate(v); err != nil {
		return invalid(err.Error())
	}
	if err = checkDecimals(v); err != nil {
		return invalid(err.Error())
	}
	// JSON Schema treats 1.0 as an integer; our Go wire DTOs intentionally
	// require lexical integers for their small integer fields as well.
	var dto any
	switch kind {
	case ManifestKind:
		dto = &Manifest{}
	case ConfigKind:
		dto = &Config{}
	case InputKind:
		dto = &Input{}
	case DecisionKind:
		dto = &Decision{}
	case ArtifactKind:
		dto = &ArtifactDescriptor{}
	}
	if err = json.Unmarshal(raw, dto); err != nil {
		return invalid(err.Error())
	}
	if kind == ManifestKind {
		m := dto.(*Manifest)
		if semver.Compare("v"+m.Core.MinInclusive, "v"+m.Core.MaxExclusive) >= 0 {
			return invalid("empty core version range")
		}
		found := false
		for _, cap := range m.RequiredCapabilities {
			if cap == HookOrderPreCreate {
				found = true
			}
		}
		if !found {
			return invalid("required order hook missing")
		}
		seen := map[string]bool{}
		for _, ui := range m.UIContributions {
			if seen[ui.ExtensionPoint] {
				return invalid("duplicate UI extension point")
			}
			seen[ui.ExtensionPoint] = true
			keys := map[string]bool{}
			for _, f := range ui.Fields {
				if keys[f.Key] {
					return invalid("duplicate UI field")
				}
				keys[f.Key] = true
				if f.Key == "enabled" && (f.Type != "switch" || f.OptionsSource != "none") {
					return invalid("enabled field must be a switch")
				}
				if f.Key == "allowedLevelIds" && (f.Type != "multiselect" || f.OptionsSource != "member-level-options.v1") {
					return invalid("invalid level selector")
				}
			}
		}
	}
	if kind == ArtifactKind {
		n, _ := strconv.ParseUint(v.(map[string]any)["archiveBytes"].(string), 10, 64)
		if n > MaxArchiveBytes {
			return invalid("archive exceeds limit")
		}
	}
	return nil
}

func invalid(detail string) error { return &Error{Code: InvalidContract, Detail: detail} }

// readValue rejects duplicate keys before decoding can silently overwrite them.
func readValue(d *json.Decoder, depth int, budget *int) (any, error) {
	*budget--
	if depth > 32 || *budget < 0 {
		return nil, fmt.Errorf("JSON complexity limit exceeded")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := make(map[string]any)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := m[k]; exists {
				return nil, fmt.Errorf("duplicate key %q", k)
			}
			v, err := readValue(d, depth+1, budget)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		_, err = d.Token()
		return m, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := readValue(d, depth+1, budget)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	default:
		if _, ok := t.(json.Delim); ok {
			return nil, fmt.Errorf("unexpected delimiter")
		}
		return t, nil
	}
}

func checkDecimals(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			switch k {
			case "revision", "generation", "subsiteId", "productId", "skuId", "effectiveLevelId", "archiveBytes":
				if _, err := strconv.ParseUint(value.(string), 10, 64); err != nil {
					return fmt.Errorf("%s exceeds uint64", k)
				}
			case "allowedLevelIds":
				for _, id := range value.([]any) {
					if _, err := strconv.ParseUint(id.(string), 10, 64); err != nil {
						return fmt.Errorf("level ID exceeds uint64")
					}
				}
			}
			if err := checkDecimals(value); err != nil {
				return err
			}
		}
	case []any:
		for _, value := range x {
			if err := checkDecimals(value); err != nil {
				return err
			}
		}
	}
	return nil
}

type Host struct {
	PaidEntitlements bool
	CoreVersion      string
	APIVersion       string
	Capabilities     map[string]bool
	UIExtensions     map[string]bool
}

// CheckCompatibility returns skipped optional UI extensions. Validation must
// precede installation as well; compatibility alone is not signature approval.
// Paid activation is deliberately unavailable until P6 adds an entitlement gate.
func CheckCompatibility(m Manifest, h Host) ([]string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err = Validate(ManifestKind, b); err != nil {
		return nil, err
	}
	v := "v" + h.CoreVersion
	if !semver.IsValid(v) || h.APIVersion != m.HostAPIVersion || semver.Compare(v, "v"+m.Core.MinInclusive) < 0 || semver.Compare(v, "v"+m.Core.MaxExclusive) >= 0 {
		return nil, &Error{Code: Incompatible, Detail: "core or host API version"}
	}
	for _, cap := range m.RequiredCapabilities {
		if !h.Capabilities[cap] {
			return nil, &Error{Code: Incompatible, Detail: "required capability: " + cap}
		}
	}
	if m.Entitlement.Mode != "free" && !h.PaidEntitlements {
		return nil, &Error{Code: PaidUnsupported, Detail: "paid activation requires P6"}
	}
	var skipped []string
	for _, ui := range m.UIContributions {
		if h.UIExtensions[ui.ExtensionPoint] {
			continue
		}
		if ui.Required {
			return nil, &Error{Code: Incompatible, Detail: "required UI: " + ui.ExtensionPoint}
		}
		skipped = append(skipped, ui.ExtensionPoint)
	}
	return skipped, nil
}

// Schema returns a copy of the installed host schema for administrative forms.
func Schema(kind Kind) ([]byte, error) {
	switch kind {
	case ManifestKind, ConfigKind, InputKind, DecisionKind, ArtifactKind:
		return schemas.ReadFile("schema/" + string(kind) + ".json")
	default:
		return nil, invalid("unknown document kind")
	}
}
