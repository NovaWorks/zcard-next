package plugincontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSchemaFixtures(t *testing.T) {
	var cases []struct {
		File  string
		Kind  Kind
		Valid bool
	}
	if err := json.Unmarshal(fixture(t, "cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.File, func(t *testing.T) {
			t.Parallel()
			err := Validate(c.Kind, fixture(t, c.File))
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v, error=%v", c.Valid, err)
			}
			if err != nil {
				var ce *Error
				if !errors.As(err, &ce) || ce.Code != InvalidContract {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestStrictJSON(t *testing.T) {
	valid := fixture(t, "decision-valid.json")
	for name, raw := range map[string][]byte{
		"duplicate-key":     []byte(`{"allow":false,"allow":true,"reason":"OK"}`),
		"escaped-duplicate": []byte(`{"allow":false,"\u0061llow":true,"reason":"OK"}`),
		"trailing":          append(append([]byte{}, valid...), []byte(`{}`)...),
		"null":              []byte(`null`),
		"invalid-utf8":      {0xff},
		"oversize":          bytes.Repeat([]byte(" "), MaxJSONBytes+1),
		"deep":              []byte(strings.Repeat("[", 34) + strings.Repeat("]", 34)),
		"empty":             {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(DecisionKind, raw); err == nil {
				t.Fatal("accepted unsafe JSON")
			}
		})
	}
	if err := Validate("remote-schema", valid); err == nil {
		t.Fatal("accepted unknown schema")
	}
}

func manifest(t *testing.T) Manifest {
	t.Helper()
	var m Manifest
	if err := json.Unmarshal(fixture(t, "manifest-valid.json"), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestWireIntegerRepresentation(t *testing.T) {
	raw := fixture(t, "input-valid.json")
	for _, replacement := range []string{`"quantity": 1.0`, `"quantity": 1e0`} {
		changed := bytes.Replace(raw, []byte(`"quantity": 1`), []byte(replacement), 1)
		if bytes.Equal(changed, raw) {
			t.Fatal("fixture quantity not found")
		}
		if err := Validate(InputKind, changed); err == nil {
			t.Fatal("accepted noncanonical integer")
		}
	}
}

func TestCompatibility(t *testing.T) {
	h := Host{CoreVersion: "1.2.89", APIVersion: "1", Capabilities: map[string]bool{HookOrderPreCreate: true}}
	t.Run("optional-ui-does-not-block-backend", func(t *testing.T) {
		skipped, err := CheckCompatibility(manifest(t), h)
		if err != nil || len(skipped) != 1 || skipped[0] != ProductEditor {
			t.Fatalf("skipped=%v err=%v", skipped, err)
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest, *Host)
		code   ErrorCode
	}{
		{"old-core", func(_ *Manifest, h *Host) { h.CoreVersion = "1.2.88" }, Incompatible},
		{"exclusive-upper", func(_ *Manifest, h *Host) { h.CoreVersion = "2.0.0" }, Incompatible},
		{"api", func(_ *Manifest, h *Host) { h.APIVersion = "2" }, Incompatible},
		{"missing-hook", func(_ *Manifest, h *Host) { h.Capabilities = nil }, Incompatible},
		{"unknown-required", func(m *Manifest, _ *Host) { m.RequiredCapabilities = append(m.RequiredCapabilities, "new.hook.v1") }, Incompatible},
		{"required-ui", func(m *Manifest, _ *Host) { m.UIContributions[0].Required = true }, Incompatible},
		{"paid", func(m *Manifest, _ *Host) { m.Entitlement.Mode = "paid" }, PaidUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, host := manifest(t), h
			tc.mutate(&m, &host)
			_, err := CheckCompatibility(m, host)
			var ce *Error
			if !errors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExactDecimalAndSignature(t *testing.T) {
	var c Config
	raw := fixture(t, "config-valid.json")
	if err := Validate(ConfigKind, raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.AllowedLevelIDs[1] != "9007199254740993" {
		t.Fatal("ID lost precision")
	}
	var d ArtifactDescriptor
	if err := json.Unmarshal(fixture(t, "artifact-valid.json"), &d); err != nil {
		t.Fatal(err)
	}
	a, err := SigningMessage(d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SigningMessage(d)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("unstable signing message")
	}
	expected := `zcard.plugin.artifact.v1` + "\n" + `{"schemaVersion":1,"pluginId":"member-purchase-gate","version":"0.1.0","manifestSHA256":"` + strings.Repeat("a", 64) + `","archiveSHA256":"` + strings.Repeat("b", 64) + `","archiveBytes":"1024","keyId":"test-artifact"}`
	if string(a) != expected {
		t.Fatalf("signing protocol changed:\n%s", a)
	}
	for _, mutate := range []func(*ArtifactDescriptor){
		func(v *ArtifactDescriptor) { v.Version = "0.1.1" },
		func(v *ArtifactDescriptor) { v.ManifestSHA256 = strings.Repeat("c", 64) },
		func(v *ArtifactDescriptor) { v.ArchiveSHA256 = strings.Repeat("d", 64) },
		func(v *ArtifactDescriptor) { v.ArchiveBytes = "1025" },
		func(v *ArtifactDescriptor) { v.KeyID = "another-key" },
		func(v *ArtifactDescriptor) { v.PluginID = "another-plugin" },
	} {
		changed := d
		mutate(&changed)
		b, err := SigningMessage(changed)
		if err != nil || bytes.Equal(a, b) {
			t.Fatalf("mutation not signed: %v", err)
		}
	}
}

// This is fixture schema validation only. Execution and side-effect assertions
// require the P2 runtime and purchase adapters; this test is not that evidence.
func TestBusinessFixtureInputs(t *testing.T) {
	var cases []struct {
		ID       string
		Input    json.RawMessage
		Expected string
	}
	if err := json.Unmarshal(fixture(t, "business-cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] {
			t.Fatal("missing/duplicate fixture ID")
		}
		seen[c.ID] = true
		if err := Validate(InputKind, c.Input); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		switch c.Expected {
		case "OK", "LOGIN_REQUIRED", "MEMBER_LEVEL_DENIED", "SUPPLY_RESTRICTED", "PLUGIN_UNAVAILABLE":
		default:
			t.Fatalf("unknown expected result: %s", c.ID)
		}
	}
}
