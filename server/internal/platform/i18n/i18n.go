// Package i18n 多语言文案（规划 §5.15）。
//
// M0：最小实现（zh_CN 默认 + en 骨架，内存 bundle）；M3 交付 embed bundle 完整版
// 与 DB 覆盖层（后台可改文案）。错误 message 走 i18n，前端按 reason 做文案与
// 重定向、不解析 message（§7.2）。
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Locale 语言标识（zh_CN / en；架构支持任意扩展）。
type Locale string

// 支持的语言。
const (
	ZhCN Locale = "zh_CN"
	En   Locale = "en"
)

// Default 默认语言（铁律 9：文案默认简体中文）。
const Default = ZhCN

//go:embed locales/*.json
var localeFS embed.FS

// Bundle 文案包（只读；M3 增加 DB 覆盖层）。
type Bundle struct {
	messages map[Locale]map[string]string
}

// Load 从 embed FS 装载全部语言。
func Load() (*Bundle, error) {
	b := &Bundle{messages: map[Locale]map[string]string{}}
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("i18n: 读取语言包失败: %w", err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		data, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, err
		}
		msgs := map[string]string{}
		if err := json.Unmarshal(data, &msgs); err != nil {
			return nil, fmt.Errorf("i18n: 解析 %s 失败: %w", e.Name(), err)
		}
		b.messages[Locale(name)] = msgs
	}
	return b, nil
}

// T 取文案：locale 缺失或 key 缺失时回退 zh_CN，再回退 key 本身（绝不返回空）。
func (b *Bundle) T(locale Locale, key string) string {
	if msgs, ok := b.messages[locale]; ok {
		if v, ok := msgs[key]; ok {
			return v
		}
	}
	if msgs, ok := b.messages[Default]; ok {
		if v, ok := msgs[key]; ok {
			return v
		}
	}
	return key
}

// FromContext 从上下文取语言（请求头 Accept-Language 的解析由 transport 中间件注入；
// worker/定时任务走 settings 站点默认语言）。
func FromContext(ctx context.Context) Locale {
	if v, ok := ctx.Value(localeKey{}).(Locale); ok && v != "" {
		return v
	}
	return Default
}

type localeKey struct{}

// WithLocale 注入语言。
func WithLocale(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, localeKey{}, l)
}
