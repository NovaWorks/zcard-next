package i18n

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Normalize maps supported language tags to the keys used by the language packs.
func Normalize(value string) Locale {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	switch value {
	case "zh", "zh-cn", "zh-hans", "zh-hans-cn":
		return ZhCN
	case "en":
		return En
	}
	if strings.HasPrefix(value, "en-") {
		return En
	}
	return ""
}

// Policy limits a request to the site's enabled languages.
type Policy struct {
	Default Locale
	Enabled []Locale
}

func NewPolicy(defaultLocale string, enabledLocales []string) Policy {
	p := Policy{Default: Normalize(defaultLocale)}
	seen := map[Locale]bool{}
	for _, value := range enabledLocales {
		if locale := Normalize(value); locale != "" && !seen[locale] {
			p.Enabled = append(p.Enabled, locale)
			seen[locale] = true
		}
	}
	if len(p.Enabled) == 0 {
		p.Enabled = []Locale{Default}
	}
	if !p.enabled(p.Default) {
		p.Default = p.Enabled[0]
	}
	return p
}

func (p Policy) enabled(locale Locale) bool {
	for _, enabled := range p.Enabled {
		if enabled == locale {
			return true
		}
	}
	return false
}

// Resolve falls back to the configured default for unsupported or disabled tags.
func (p Policy) Resolve(value string) Locale {
	if locale := Normalize(value); p.enabled(locale) {
		return locale
	}
	return p.Default
}

// AcceptLanguage honors supported, enabled tags and their quality weights.
func (p Policy) AcceptLanguage(value string) Locale {
	locale, best := p.Default, float64(0)
	for _, part := range strings.Split(value, ",") {
		tokens := strings.Split(part, ";")
		candidate := Normalize(tokens[0])
		if !p.enabled(candidate) {
			continue
		}
		quality := float64(1)
		for _, token := range tokens[1:] {
			key, raw, ok := strings.Cut(strings.TrimSpace(token), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if quality > best {
			locale, best = candidate, quality
		}
	}
	return locale
}

type policyKey struct{}

func WithPolicy(ctx context.Context, policy Policy) context.Context {
	return WithLocale(context.WithValue(ctx, policyKey{}, policy), policy.Default)
}

// ResolveContext uses the request locale when no explicit content locale was supplied.
func ResolveContext(ctx context.Context, value string) Locale {
	if value == "" {
		return FromContext(ctx)
	}
	if policy, ok := ctx.Value(policyKey{}).(Policy); ok {
		return policy.Resolve(value)
	}
	if locale := Normalize(value); locale != "" {
		return locale
	}
	return FromContext(ctx)
}

func HTMLLanguage(locale Locale) string {
	if locale == En {
		return "en"
	}
	return "zh-CN"
}

// Value selects merchant-authored translations, then Chinese, then a stable nonempty value.
func Value(values map[string]string, locale string) string {
	if value := values[locale]; value != "" {
		return value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, preferred := range []Locale{Normalize(locale), ZhCN} {
		if preferred == "" {
			continue
		}
		if value := values[string(preferred)]; value != "" {
			return value
		}
		for _, key := range keys {
			if Normalize(key) == preferred && values[key] != "" {
				return values[key]
			}
		}
	}
	for _, key := range keys {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}
