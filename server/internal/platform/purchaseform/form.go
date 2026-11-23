// Package purchaseform describes supplier fields without depending on storage or HTTP.
package purchaseform

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ConfigKey = "upstream_purchase_form"

type Field struct {
	Key          string            `json:"key"`
	Type         string            `json:"type"`
	Label        string            `json:"label"`
	Placeholder  string            `json:"placeholder"`
	Required     bool              `json:"required"`
	Options      []string          `json:"options,omitempty"`
	OptionLabels map[string]string `json:"option_labels,omitempty"`
	Regex        string            `json:"regex,omitempty"`
	Min          *float64          `json:"min,omitempty"`
	Max          *float64          `json:"max,omitempty"`
	MaxLength    int               `json:"max_length"`
}

type Binding struct {
	ControlID string `json:"control_id"`
	Field     Field  `json:"field"`
}

type Contract struct {
	ConnectionID uint64    `json:"connection_id"`
	ProductCode  string    `json:"product_code"`
	Fields       []Binding `json:"fields"`
}

func Decode(value any) (*Contract, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var c Contract
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func ParseSchema(raw json.RawMessage) ([]Field, error) {
	if len(raw) == 0 {
		return nil, nil
	} // old peers omit the schema entirely
	var schema struct {
		Fields []struct {
			Key, Type, Regex   string
			Label, Placeholder any
			Required           bool
			Options            []string
			OptionLabels       map[string]string `json:"option_labels"`
			Min, Max           *float64
			MaxLen             *int `json:"max_len"`
			MaxLength          *int `json:"max_length"`
		}
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("上游填写规则格式无效")
	}
	if len(schema.Fields) > 100 {
		return nil, fmt.Errorf("上游填写字段过多")
	}
	fields := make([]Field, 0, len(schema.Fields))
	seen := map[string]bool{}
	for _, v := range schema.Fields {
		f := Field{Key: v.Key, Type: v.Type, Label: localized(v.Label), Placeholder: localized(v.Placeholder), Required: v.Required, Options: v.Options, OptionLabels: v.OptionLabels, Regex: v.Regex, Min: v.Min, Max: v.Max, MaxLength: 4000}
		if !regexp.MustCompile(`^[a-z0-9_]{1,64}$`).MatchString(f.Key) || seen[f.Key] {
			return nil, fmt.Errorf("上游填写字段标识无效或重复")
		}
		seen[f.Key] = true
		switch f.Type {
		case "text", "password", "textarea", "phone", "email", "number", "select", "radio", "checkbox":
		default:
			return nil, fmt.Errorf("上游填写字段类型暂不支持")
		}
		if f.Label == "" {
			f.Label = f.Key
		}
		if utf8.RuneCountInString(f.Label) > 60 {
			f.Label = string([]rune(f.Label)[:60])
		}
		if v.MaxLength != nil {
			v.MaxLen = v.MaxLength
		}
		if v.MaxLen != nil {
			if *v.MaxLen <= 0 {
				return nil, fmt.Errorf("上游长度规则无效")
			}
			f.MaxLength = min(*v.MaxLen, 4000)
		}
		if f.Regex != "" {
			if _, err := compileRegex(f.Regex); err != nil {
				return nil, fmt.Errorf("上游字段校验表达式暂不支持")
			}
		}
		if (f.Min != nil && (math.IsNaN(*f.Min) || math.IsInf(*f.Min, 0))) || (f.Max != nil && (math.IsNaN(*f.Max) || math.IsInf(*f.Max, 0))) || (f.Min != nil && f.Max != nil && *f.Min > *f.Max) {
			return nil, fmt.Errorf("上游数字范围无效")
		}
		if (f.Type == "select" || f.Type == "radio" || f.Type == "checkbox") && len(f.Options) == 0 {
			return nil, fmt.Errorf("上游选项缺失")
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func (f Field) ControlType() string {
	switch f.Type {
	case "textarea", "password", "number", "select", "radio", "checkbox":
		return f.Type
	default:
		return "text"
	}
}

func (f Field) Value(value string) (any, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		if f.Required {
			return nil, fmt.Errorf("请填写此项")
		}
		return nil, nil
	}
	if f.Type=="password" || f.Type=="textarea" {v=value}
 if utf8.RuneCountInString(v) > f.MaxLength {
		return nil, fmt.Errorf("填写内容超过长度限制")
	}
	if f.Regex != "" {
		re, err := compileRegex(f.Regex)
		if err != nil || !re.MatchString(v) {
			return nil, fmt.Errorf("填写格式不符合上游要求")
		}
	}
	switch f.Type {
	case "number":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
			return nil, fmt.Errorf("数字不在允许范围内")
		}
		return n, nil
	case "email":
		if _, err := mail.ParseAddress(v); err != nil {
			return nil, fmt.Errorf("邮箱格式无效")
		}
	case "phone":
		if !regexp.MustCompile(`^\+?[0-9\-()\s]{6,20}$`).MatchString(v) {
			return nil, fmt.Errorf("电话号码格式无效")
		}
	case "select", "radio", "checkbox":
		values := []string{v}
		if f.Type == "checkbox" {
			values = strings.Split(v, ",")
		}
		seen := map[string]bool{}
		for _, x := range values {
			found := false
			for _, option := range f.Options {
				if x == option {
					found = true
					break
				}
			}
			if !found || seen[x] {
				return nil, fmt.Errorf("请选择有效选项")
			}
			seen[x] = true
		}
		if f.Type == "checkbox" {
			return values, nil
		}
	}
	return v, nil
}

func compileRegex(raw string) (*regexp.Regexp, error) {
	if strings.HasPrefix(raw, "/") {
		end := strings.LastIndex(raw, "/")
		if end > 0 {
			flags := ""
			for _, flag := range raw[end+1:] {
				if !strings.ContainsRune("imsu", flag) {
					return nil, fmt.Errorf("unsupported regex flag")
				}
				if flag != 'u' {
					flags += string(flag)
				}
			}
			raw = raw[1:end]
			if flags != "" {
				raw = "(?" + flags + ")" + raw
			}
		}
	}
	return regexp.Compile(raw)
}

func localized(v any) string {
	if text, ok := v.(string); ok {
		return strings.TrimSpace(text)
	}
	if values, ok := v.(map[string]any); ok {
		for _, key := range []string{"zh-CN", "zh-TW", "zh", "en-US", "en"} {
			if text, ok := values[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}
