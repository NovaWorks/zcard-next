package settings

import (
	"context"
	"encoding/json"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"github.com/go-kratos/kratos/v3/errors"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

type themePreview struct {
	Runtime *theme.Runtime
	Expires time.Time
	Site    uint64
}

var themePreviews = struct {
	sync.Mutex
	items map[string]themePreview
}{items: map[string]themePreview{}}

func (s *AdminSettingsService) PreviewThemeSettings(ctx context.Context, req *adminv1.SaveThemeSettingsRequest) (*adminv1.ThemePreviewReply, error) {
	themeChanges.RLock()
	defer themeChanges.RUnlock()
	t, schema, err := resolveSettingsTheme(req.Key, req.ThemeRevision)
	if err != nil || schema == nil {
		return nil, errors.BadRequest("theme.PREVIEW_UNAVAILABLE", "主题版本或扩展设置不可用")
	}
	var values map[string]any
	if len(req.ValuesJson) > 128<<10 || json.Unmarshal([]byte(req.ValuesJson), &values) != nil {
		return nil, errors.BadRequest("theme.INVALID_VALUES", "设置格式无效")
	}
	if err = schema.ValidateValues(values); err != nil {
		return nil, errors.BadRequest("theme.INVALID_VALUES", err.Error())
	}
	st, err := readThemeState(ctx, s.uc.repo, req.Key)
	if err != nil {
		return nil, err
	}
	if st.Revision != req.ExpectedRevision {
		return nil, errors.Conflict("theme.CONFLICT", "设置已更新，请重新加载")
	}
	runtime := &theme.Runtime{Key: req.Key, ThemeRevision: revisionOf(t), Values: values, Capabilities: themeCapabilities(ctx, s.uc.repo), Preview: true, Theme: t}
	token := newThemeRevision() + newThemeRevision()
	expires := time.Now().Add(15 * time.Minute)
	themePreviews.Lock()
	defer themePreviews.Unlock()
	for k, v := range themePreviews.items {
		if time.Now().After(v.Expires) {
			delete(themePreviews.items, k)
		}
	}
	if len(themePreviews.items) >= 256 {
		return nil, errors.TooManyRequests("theme.PREVIEW_LIMIT", "预览过多，请稍后再试")
	}
	themePreviews.items[token] = themePreview{Runtime: runtime, Expires: expires, Site: tenancy.FromContext(ctx).SubsiteID}
	return &adminv1.ThemePreviewReply{Url: "/?theme_preview=" + token, ExpiresAt: expires.Unix()}, nil
}

// PreviewWriteGuard is applied before API routing; preview requests are always read-only.
func PreviewWriteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Header.Get("X-Theme-Preview") != "" || r.URL.Query().Get("theme_preview") != "") && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			http.Error(w, "主题预览为只读", http.StatusForbidden)
			return
		}
		token := r.Header.Get("X-Theme-Preview")
		if token != "" {
			themePreviews.Lock()
			preview, ok := themePreviews.items[token]
			themePreviews.Unlock()
			if !ok || time.Now().After(preview.Expires) || preview.Site != tenancy.FromContext(r.Context()).SubsiteID {
				http.Error(w, "预览已过期", http.StatusGone)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			r = r.WithContext(theme.WithRuntime(r.Context(), preview.Runtime))
		}
		next.ServeHTTP(w, r)
	})
}
func (s *AdminSettingsService) runtime(ctx context.Context) (*theme.Runtime, error) {
	themeChanges.RLock()
	defer themeChanges.RUnlock()
	active := s.activeThemeUnlocked(ctx)
	key := "classic"
	if active != nil {
		key = active.Key
	}
	schema, err := theme.LoadSettings(active)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		return nil, nil
	}
	st, err := readThemeState(ctx, s.uc.repo, key)
	if err != nil {
		return nil, err
	}
	if tenancy.FromContext(ctx).SubsiteID != 0 && st.Published != nil && st.Published.ThemeRevision != revisionOf(active) {
		var e error
		active, schema, e = resolveSettingsTheme(key, st.Published.ThemeRevision)
		if e != nil {
			return nil, e
		}
	}
	values := map[string]any{}
	if st.Published != nil {
		values = schema.Normalize(st.Published.Values)
	}
	return &theme.Runtime{Key: key, ThemeRevision: revisionOf(active), ConfigRevision: st.Revision, Values: values, Capabilities: themeCapabilities(ctx, s.uc.repo), Theme: active}, nil
}
func (s *AdminSettingsService) ThemeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for _, prefix := range []string{"/api", "/assets", "/templates", "/uploads", "/health", "/install", "/payments"} {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}
		if ext := path.Ext(p); ext != "" && ext != ".html" {
			next.ServeHTTP(w, r)
			return
		}
		var runtime *theme.Runtime
		var err error
		if token := r.URL.Query().Get("theme_preview"); token != "" {
			themePreviews.Lock()
			preview, ok := themePreviews.items[token]
			themePreviews.Unlock()
			if !ok || time.Now().After(preview.Expires) || preview.Site != tenancy.FromContext(r.Context()).SubsiteID {
				http.Error(w, "预览已过期，请返回后台重新预览", http.StatusGone)
				return
			}
			runtime = preview.Runtime
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			w.Header().Set("Cache-Control", "no-store")
		} else {
			runtime, err = s.runtime(r.Context())
			if err != nil {
				http.Error(w, "主题设置读取失败，请稍后重试", http.StatusServiceUnavailable)
				return
			}
		}
		if runtime != nil {
			r = r.WithContext(theme.WithRuntime(r.Context(), runtime))
		}
		next.ServeHTTP(w, r)
	})
}
