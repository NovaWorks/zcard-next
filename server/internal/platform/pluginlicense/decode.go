package pluginlicense

import (
	"bytes"
	"encoding/json"
	"golang.org/x/mod/semver"
	"io"
	"unicode/utf8"
)

func validVersion(v string) bool { return semver.IsValid("v" + v) }

// Decode rejects duplicate keys, unknown fields, trailing data and excessive
// nesting before unmarshalling a security document. A response cannot smuggle
// alternate interpretations to another implementation or its audit log.
func Decode(raw []byte, out any) error { return DecodeLimit(raw, out, MaxDocumentBytes) }

func DecodeLimit(raw []byte, out any, limit int) error {
	if limit < 1 || limit > 2<<20 {
		return ErrInvalid
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || len(raw) > limit || !utf8.Valid(raw) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	budget := 65536
	if err := unique(d, 0, &budget); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrInvalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func unique(d *json.Decoder, depth int, budget *int) error {
	*budget--
	if depth > 16 || *budget < 0 {
		return ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	switch t {
	case json.Delim('{'):
		keys := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return ErrInvalid
			}
			k, ok := t.(string)
			if !ok || keys[k] {
				return ErrInvalid
			}
			keys[k] = true
			if err := unique(d, depth+1, budget); err != nil {
				return err
			}
		}
		if t, err := d.Token(); err != nil || t != json.Delim('}') {
			return ErrInvalid
		}
	case json.Delim('['):
		for d.More() {
			if err := unique(d, depth+1, budget); err != nil {
				return err
			}
		}
		if t, err := d.Token(); err != nil || t != json.Delim(']') {
			return ErrInvalid
		}
	default:
		if _, ok := t.(json.Delim); ok {
			return ErrInvalid
		}
	}
	return nil
}
