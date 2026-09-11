package settings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"github.com/go-kratos/kratos/v3/errors"
)

const themeStateGroup = "_theme_settings"

type themeSnapshot struct {
	ThemeRevision string         `json:"theme_revision"`
	Values        map[string]any `json:"values"`
	SavedAt       int64          `json:"saved_at"`
}
type themeOptionsState struct {
	Revision  string         `json:"revision"`
	Draft     *themeSnapshot `json:"draft,omitempty"`
	Published *themeSnapshot `json:"published,omitempty"`
	Previous  *themeSnapshot `json:"previous,omitempty"`
}
type themeStateWriter interface {
	PutThemeState(context.Context, string, string, json.RawMessage, []port.Item) error
}

func themeStateKey(ctx context.Context, key string) string {
	return fmt.Sprintf("%d:%s", tenancy.FromContext(ctx).SubsiteID, key)
}
func newThemeRevision() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func resolveSettingsTheme(key, revision string) (*theme.Theme, *theme.SettingsSchema, error) {
	if key == "classic" {
		if revision != "" && revision != "builtin" {
			return nil, nil, fmt.Errorf("内置主题版本不可用")
		}
		return nil, theme.ClassicSettings(), nil
	}
	if !theme.ValidKey(key) {
		return nil, nil, fmt.Errorf("主题标识无效")
	}
	var t *theme.Theme
	var err error
	if revision == "" {
		t, err = theme.Resolve(key)
	} else {
		t, err = theme.ResolveRevision(key, revision)
	}
	if err != nil {
		return nil, nil, err
	}
	schema, err := theme.LoadSettings(t)
	return t, schema, err
}
func revisionOf(t *theme.Theme) string {
	if t == nil {
		return "builtin"
	}
	return t.Revision
}
func inheritedValues(ctx context.Context, r Repo, schema *theme.SettingsSchema) map[string]any {
	values := schema.Defaults()
	for key := range values {
		if len(key) > 9 && key[:9] == "template." {
			if raw, err := r.Get(ctx, "template", key[9:]); err == nil {
				var v any
				if json.Unmarshal(raw, &v) == nil {
					values[key] = v
				}
			}
		}
	}
	return schema.Normalize(values)
}
func readThemeState(ctx context.Context, r Repo, key string) (*themeOptionsState, error) {
	state := &themeOptionsState{}
	raw, err := r.Get(ctx, themeStateGroup, themeStateKey(ctx, key))
	if err != nil && err != ErrSettingNotFound {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, state); err != nil {
			return nil, err
		}
	}
	return state, nil
}
func themeCapabilities(ctx context.Context, r Repo) map[string]bool {
	out := map[string]bool{}
	for name, pair := range map[string][2]string{"cart": {"trade", "cart_enabled"}, "reviews": {"template", "show_reviews"}, "recharge": {"recharge", "enabled"}, "affiliate": {"affiliate", "enabled"}} {
		val := false
		g, _ := Group(pair[0])
		if g != nil {
			if b, ok := g.DefaultJSON(pair[1]); ok {
				_ = json.Unmarshal([]byte(b), &val)
			}
		}
		if raw, err := r.Get(ctx, pair[0], pair[1]); err == nil {
			_ = json.Unmarshal(raw, &val)
		}
		out[name] = val
	}
	return out
}
func (s *AdminSettingsService) GetThemeSettings(ctx context.Context, req *adminv1.ThemeSettingsRequest) (*adminv1.ThemeSettingsReply, error) {
	themeChanges.RLock()
	defer themeChanges.RUnlock()
	return s.themeSettingsReply(ctx, req.Key, req.ThemeRevision)
}
func (s *AdminSettingsService) themeSettingsReply(ctx context.Context, key, revision string) (*adminv1.ThemeSettingsReply, error) {
	t, schema, err := resolveSettingsTheme(key, revision)
	if err != nil {
		return nil, errors.BadRequest("theme.INVALID", err.Error())
	}
	st, err := readThemeState(ctx, s.uc.repo, key)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if schema != nil {
		values = inheritedValues(ctx, s.uc.repo, schema)
		if st.Published != nil {
			values = schema.Normalize(st.Published.Values)
		}
		if st.Draft != nil {
			values = schema.Normalize(st.Draft.Values)
		}
	}
	active, _ := readActiveTheme(ctx, s.uc.repo)
	if active == nil {
		k, _ := legacySelectedTheme(ctx, s.uc.repo)
		active = &theme.Selection{Key: k}
	}
	body := map[string]any{"key": key, "theme_revision": revisionOf(t), "schema": schema, "revision": st.Revision, "values": values, "published": st.Published, "draft": st.Draft, "can_rollback": st.Previous != nil, "capabilities": themeCapabilities(ctx, s.uc.repo), "active": active != nil && active.Key == key, "versions": theme.SettingsVersions(key)}
	raw, _ := json.Marshal(body)
	return &adminv1.ThemeSettingsReply{StateJson: string(raw)}, nil
}
func (s *AdminSettingsService) SaveThemeSettings(ctx context.Context, req *adminv1.SaveThemeSettingsRequest) (*adminv1.ThemeSettingsReply, error) {
	themeChanges.Lock()
	defer themeChanges.Unlock()
	t, schema, err := resolveSettingsTheme(req.Key, req.ThemeRevision)
	if err != nil {
		return nil, errors.BadRequest("theme.INVALID", err.Error())
	}
	if schema == nil {
		return nil, errors.BadRequest("theme.UNSUPPORTED", "此主题未声明扩展设置")
	}
	st, err := readThemeState(ctx, s.uc.repo, req.Key)
	if err != nil {
		return nil, err
	}
	if st.Revision != req.ExpectedRevision {
		return nil, errors.Conflict("theme.CONFLICT", "设置已被其他页面更新，请重新加载")
	}
	values := map[string]any{}
	if req.Action == "reset" {
		values = schema.Defaults()
	} else if req.Action != "rollback" {
		if len(req.ValuesJson) > 128<<10 || json.Unmarshal([]byte(req.ValuesJson), &values) != nil {
			return nil, errors.BadRequest("theme.INVALID_VALUES", "设置格式无效")
		}
		if err = schema.ValidateValues(values); err != nil {
			return nil, errors.BadRequest("theme.INVALID_VALUES", err.Error())
		}
	}
	snap := &themeSnapshot{ThemeRevision: revisionOf(t), Values: values, SavedAt: time.Now().Unix()}
	var extra []port.Item
	switch req.Action {
	case "draft", "reset":
		st.Draft = snap
	case "publish":
		if st.Published == nil {
			baselineTheme, baselineSchema := t, schema
			if current := s.activeThemeUnlocked(ctx); current != nil && current.Key == req.Key {
				if currentSchema, e := theme.LoadSettings(current); e == nil && currentSchema != nil {
					baselineTheme, baselineSchema = current, currentSchema
				}
			}
			st.Published = &themeSnapshot{ThemeRevision: revisionOf(baselineTheme), Values: inheritedValues(ctx, s.uc.repo, baselineSchema)}
		}
		st.Previous = st.Published
		st.Published = snap
		st.Draft = nil
	case "rollback":
		if st.Previous == nil {
			return nil, errors.BadRequest("theme.NO_PREVIOUS", "没有可恢复的已发布版本")
		}
		if _, prevSchema, e := resolveSettingsTheme(req.Key, st.Previous.ThemeRevision); e != nil || prevSchema == nil {
			return nil, errors.BadRequest("theme.PREVIOUS_MISSING", "上一版主题文件不可用")
		}
		st.Published, st.Previous = st.Previous, st.Published
		st.Draft = nil
	default:
		return nil, errors.BadRequest("theme.INVALID_ACTION", "不支持的设置操作")
	}
	if req.Action == "publish" || req.Action == "rollback" {
		active, e := readActiveTheme(ctx, s.uc.repo)
		if e != nil {
			return nil, e
		}
		if active == nil {
			key, _ := legacySelectedTheme(ctx, s.uc.repo)
			active = &theme.Selection{Key: key}
		}
		if active.Key == req.Key && tenancy.FromContext(ctx).SubsiteID == 0 {
			active.Revision = st.Published.ThemeRevision
			if req.Key == "classic" {
				active.Revision = ""
			}
			raw, _ := json.Marshal(active)
			extra = append(extra, port.Item{Group: "template", Key: activeThemeKey, Value: raw})
		}
	}
	st.Revision = newThemeRevision()
	raw, _ := json.Marshal(st)
	if writer, ok := s.uc.repo.(themeStateWriter); ok {
		err = writer.PutThemeState(ctx, themeStateKey(ctx, req.Key), req.ExpectedRevision, raw, extra)
	} else {
		extra = append(extra, port.Item{Group: themeStateGroup, Key: themeStateKey(ctx, req.Key), Value: raw})
		err = s.uc.repo.PutMany(ctx, extra)
	}
	if err != nil {
		return nil, err
	}
	replyRevision := req.ThemeRevision
	if req.Action == "rollback" {
		replyRevision = st.Published.ThemeRevision
	}
	return s.themeSettingsReply(ctx, req.Key, replyRevision)
}
