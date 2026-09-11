package theme

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

// SettingsSchema describes declarative controls. Theme code never runs in the admin.
type SettingsSchema struct {
	Version int             `json:"version"`
	Groups  []SettingsGroup `json:"groups"`
}
type SettingsGroup struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Fields []SettingsField `json:"fields"`
}
type SettingsOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type SettingsCondition struct {
	Field  string `json:"field"`
	Equals any    `json:"equals"`
}
type SettingsField struct {
	RenamedFrom string             `json:"renamed_from,omitempty"`
	Key         string             `json:"key"`
	Label       string             `json:"label"`
	Type        string             `json:"type"`
	Default     any                `json:"default"`
	Help        string             `json:"help,omitempty"`
	Min         *float64           `json:"min,omitempty"`
	Max         *float64           `json:"max,omitempty"`
	Options     []SettingsOption   `json:"options,omitempty"`
	VisibleWhen *SettingsCondition `json:"visible_when,omitempty"`
	Capability  string             `json:"capability,omitempty"`
}

var settingKey = regexp.MustCompile(`^(theme|template)\.[a-z][a-z0-9_]{0,60}$`)
var settingColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var legacyThemeKeys = map[string]bool{"template.bg_image": true, "template.bg_image_mobile": true, "template.category_nav_style": true, "template.default_view": true, "template.per_row": true, "template.per_page": true, "template.sort_by": true, "template.show_stock": true, "template.show_sales": true}

func LoadSettings(t *Theme) (*SettingsSchema, error) {
	if t == nil {
		return ClassicSettings(), nil
	}
	if t.SettingsSchema == "" {
		return nil, nil
	}
	root, err := os.OpenRoot(t.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	b, err := root.ReadFile(t.SettingsSchema)
	if err != nil {
		return nil, err
	}
	if len(b) > 128<<10 {
		return nil, fmt.Errorf("主题设置定义超过 128KB")
	}
	var s SettingsSchema
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("主题设置定义不是合法 JSON")
	}
	if err = s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}
func (s *SettingsSchema) Validate() error {
	if s.Version != 1 || len(s.Groups) == 0 || len(s.Groups) > 20 {
		return fmt.Errorf("不支持的设置版本或分组数量")
	}
	seen := map[string]bool{}
	groups := map[string]bool{}
	total := 0
	for _, g := range s.Groups {
		if g.ID == "" || groups[g.ID] || g.Label == "" {
			return fmt.Errorf("设置分组标识和名称必须有效且唯一")
		}
		groups[g.ID] = true
		for _, f := range g.Fields {
			total++
			if !settingKey.MatchString(f.Key) || seen[f.Key] || f.Label == "" {
				return fmt.Errorf("设置字段无效或重复: %s", f.Key)
			}
			if strings.HasPrefix(f.Key, "template.") && !legacyThemeKeys[f.Key] {
				return fmt.Errorf("主题不可覆盖业务或系统设置: %s", f.Key)
			}
			if f.RenamedFrom != "" && (!settingKey.MatchString(f.RenamedFrom) || f.RenamedFrom == f.Key) {
				return fmt.Errorf("无效的旧字段名: %s", f.RenamedFrom)
			}
			if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
				return fmt.Errorf("字段范围无效: %s", f.Key)
			}
			seen[f.Key] = true
			if f.Capability != "" && f.Capability != "cart" && f.Capability != "reviews" && f.Capability != "recharge" && f.Capability != "affiliate" {
				return fmt.Errorf("未知业务能力: %s", f.Capability)
			}
			if err := f.ValidateValue(f.Default); err != nil {
				return err
			}
		}
	}
	if total > 120 {
		return fmt.Errorf("主题设置最多 120 项")
	}
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			if f.RenamedFrom != "" && seen[f.RenamedFrom] {
				return fmt.Errorf("旧字段仍在当前定义中")
			}
			if f.VisibleWhen != nil && !seen[f.VisibleWhen.Field] {
				return fmt.Errorf("条件引用了未知字段")
			}
		}
	}
	return nil
}
func (f SettingsField) ValidateValue(v any) error {
	bad := func() error { return fmt.Errorf("%s：设置值不符合字段要求", f.Label) }
	switch f.Type {
	case "switch":
		if _, ok := v.(bool); !ok {
			return bad()
		}
	case "number", "product", "category":
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return bad()
		}
		if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
			return bad()
		}
		if f.Type != "number" && (n < 0 || n != float64(int64(n))) {
			return bad()
		}
	case "select":
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		found := false
		for _, o := range f.Options {
			if o.Value == s {
				found = true
			}
		}
		if !found {
			return bad()
		}
	case "color":
		s, ok := v.(string)
		if !ok || !settingColor.MatchString(s) {
			return bad()
		}
	case "text", "textarea", "image":
		s, ok := v.(string)
		if !ok || len(s) > 12000 {
			return bad()
		}
		if f.Type == "image" && s != "" && !strings.HasPrefix(s, "/uploads/") && !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
			return bad()
		}
	default:
		return fmt.Errorf("未知设置控件: %s", f.Type)
	}
	return nil
}
func (s *SettingsSchema) Defaults() map[string]any {
	m := map[string]any{}
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			m[f.Key] = f.Default
		}
	}
	return m
}

// Normalize keeps valid previous values and supplies defaults for added/changed fields.
func (s *SettingsSchema) Normalize(old map[string]any) map[string]any {
	m := s.Defaults()
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			v, ok := old[f.Key]
			if !ok && f.RenamedFrom != "" {
				v, ok = old[f.RenamedFrom]
			}
			if ok && f.ValidateValue(v) == nil {
				m[f.Key] = v
			}
		}
	}
	return m
}
func (s *SettingsSchema) ValidateValues(values map[string]any) error {
	known := map[string]bool{}
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			known[f.Key] = true
			if v, ok := values[f.Key]; ok {
				if err := f.ValidateValue(v); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("缺少设置: %s", f.Label)
			}
		}
	}
	for k := range values {
		if !known[k] {
			return fmt.Errorf("未知设置: %s", k)
		}
	}
	return nil
}

type SettingsVersion struct {
	Revision string `json:"revision"`
	Version  string `json:"version"`
}

func SettingsVersions(key string) []SettingsVersion {
	if key == "classic" {
		return []SettingsVersion{{"builtin", "内置主题"}}
	}
	var out []SettingsVersion
	entries, err := os.ReadDir(Root + "/" + key)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && revisionPattern.MatchString(e.Name()) {
			if t, err := ResolveRevision(key, e.Name()); err == nil {
				out = append(out, SettingsVersion{t.Revision, t.Version})
			}
		}
	}
	return out
}
