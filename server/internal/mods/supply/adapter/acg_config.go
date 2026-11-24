package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PHP forks return either raw INI or its parsed object; [] represents an empty
// PHP array. Unknown structures must not silently turn a SKU product into plain goods.
type acgConfig string

func (c *acgConfig) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		*c = ""
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readAcgConfigValue(decoder, 0)
	if err != nil {
		return fmt.Errorf("ACG 规格配置格式无效")
	}
	if text, ok := value.(string); ok {
		*c = acgConfig(text)
		return nil
	}
	if empty, ok := value.([]any); ok && len(empty) == 0 {
		*c = ""
		return nil
	}
	sections, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("ACG 规格配置必须为 INI 文本或配置对象")
	}
	var out strings.Builder
	for _, section := range sortedAcgKeys(sections) {
		switch section {
		case "category", "category_factory", "sku", "wholesale", "category_wholesale":
		default:
			return fmt.Errorf("ACG 规格配置包含不支持的段落")
		}
		entries := map[string]string{}
		if err := flattenAcgConfig(sections[section], section, "", entries); err != nil {
			return err
		}
		// An explicitly empty spec section cannot authorize a plain-product quote.
		if len(entries) == 0 && (section == "category" || section == "category_factory" || section == "sku") {
			return fmt.Errorf("ACG 规格配置段落为空")
		}
		out.WriteString("[" + section + "]\n")
		for _, key := range sortedAcgKeys(entries) {
			out.WriteString(key + "=" + entries[key] + "\n")
		}
	}
	*c = acgConfig(out.String())
	return nil
}

func sortedAcgKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func flattenAcgConfig(value any, section, prefix string, out map[string]string) error {
	entries, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("ACG 规格配置段落必须为对象")
	}
	for _, key := range sortedAcgKeys(entries) {
		if key == "" || key != strings.TrimSpace(key) || strings.ContainsAny(key, "\r\n=[];#") {
			return fmt.Errorf("ACG 规格配置键无效")
		}
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		if _, nested := entries[key].(map[string]any); nested {
			if section == "category" || section == "category_factory" || (section == "sku" && (prefix != "" || strings.Contains(key, "."))) {
				return fmt.Errorf("ACG 规格配置嵌套无效")
			}
			if err := flattenAcgConfig(entries[key], section, name, out); err != nil {
				return err
			}
			continue
		}
		var text string
		switch v := entries[key].(type) {
		case string:
			text = v
		case json.Number:
			text = v.String()
		default:
			return fmt.Errorf("ACG 规格配置值无效")
		}
		if text == "" || strings.ContainsAny(text, "\r\n") {
			return fmt.Errorf("ACG 规格配置值无效")
		}
		if section == "sku" {
			dot := strings.Index(name, ".")
			if dot <= 0 || dot == len(name)-1 {
				return fmt.Errorf("ACG 规格配置缺少规格名称或选项")
			}
		}
		if _, duplicate := out[name]; duplicate {
			return fmt.Errorf("ACG 规格配置键重复")
		}
		out[name] = text
	}
	return nil
}

// Preserve decimal lexemes and reject duplicate JSON keys before normalization.
func readAcgConfigValue(d *json.Decoder, depth int) (any, error) {
	if depth > 6 {
		return nil, fmt.Errorf("config nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		out := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid config key")
			}
			if _, exists := out[name]; exists {
				return nil, fmt.Errorf("duplicate config key")
			}
			out[name], err = readAcgConfigValue(d, depth+1)
			if err != nil {
				return nil, err
			}
		}
		_, err = d.Token()
		return out, err
	case json.Delim('['):
		if d.More() {
			return nil, fmt.Errorf("nonempty config array")
		}
		_, err = d.Token()
		return []any{}, err
	default:
		return token, nil
	}
}
