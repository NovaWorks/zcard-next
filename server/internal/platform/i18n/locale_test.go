package i18n

import (
	"context"
	"testing"
)

func TestLanguagePolicy(t *testing.T) {
	p := NewPolicy("en_AU", []string{"zh-CN", "en", "en-US", "fr"})
	if p.Default != En || len(p.Enabled) != 2 {
		t.Fatalf("unexpected policy: %+v", p)
	}
	for _, tc := range []struct {
		header string
		want   Locale
	}{
		{"", En}, {"fr", En}, {"zh_CN", ZhCN}, {"en-US;q=0.4,zh-CN;q=0.8", ZhCN},
		{"fr,zh-CN;q=0.5", ZhCN}, {"zh-CN;q=0", En}, {"zh-CN;q=bad", En},
	} {
		if got := p.AcceptLanguage(tc.header); got != tc.want {
			t.Errorf("header %q: got %s, want %s", tc.header, got, tc.want)
		}
	}
	chineseOnly := NewPolicy("zh_CN", []string{"zh_CN"})
	if chineseOnly.Resolve("en-US") != ZhCN || chineseOnly.AcceptLanguage("en,zh;q=0.5") != ZhCN {
		t.Fatal("disabled language was accepted")
	}
	if got := NewPolicy("en", []string{"unknown"}); got.Default != ZhCN || len(got.Enabled) != 1 {
		t.Fatalf("invalid configuration did not recover: %+v", got)
	}
	ctx := WithLocale(WithPolicy(context.Background(), p), ZhCN)
	if ResolveContext(ctx, "") != ZhCN || ResolveContext(ctx, "fr") != En || ResolveContext(ctx, "en-US") != En {
		t.Fatal("content request did not respect the request locale and site policy")
	}
}

func TestMerchantLanguageAliasesAndFallback(t *testing.T) {
	values := map[string]string{"zh": "中文", "en_US": "English", "fr": "Français"}
	if Value(values, "en-AU") != "English" || Value(values, "ja") != "中文" {
		t.Fatal("merchant language aliases did not resolve")
	}
	if Value(map[string]string{"en": "", "zh_CN": "中文"}, "en") != "中文" {
		t.Fatal("empty translation did not fall back to Chinese")
	}
	if HTMLLanguage(En) != "en" || HTMLLanguage(ZhCN) != "zh-CN" {
		t.Fatal("invalid HTML language tag")
	}
}
