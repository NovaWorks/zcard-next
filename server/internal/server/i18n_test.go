package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type languageSettings map[string]json.RawMessage

func (s languageSettings) Get(_ context.Context, group, key string) (json.RawMessage, error) {
	return s[group+"."+key], nil
}

func (s languageSettings) GetDefault(ctx context.Context, group, key string, fallback json.RawMessage) (json.RawMessage, error) {
	if value, _ := s.Get(ctx, group, key); len(value) != 0 {
		return value, nil
	}
	return fallback, nil
}

type languageContentStub struct {
	storefrontv1.UnimplementedStoreContentServiceServer
}

func (languageContentStub) ListPosts(ctx context.Context, req *storefrontv1.ListPostsRequest) (*storefrontv1.ListPostsReply, error) {
	return &storefrontv1.ListPostsReply{Posts: []*storefrontv1.StorePost{{Title: string(i18n.ResolveContext(ctx, req.GetLocale()))}}}, nil
}

func TestI18nMiddlewareFollowsLiveSettings(t *testing.T) {
	settings := languageSettings{
		"i18n.default_locale":  json.RawMessage(`"en"`),
		"i18n.enabled_locales": json.RawMessage(`["zh_CN","en"]`),
	}
	srv := khttp.NewServer(khttp.Middleware(i18nMiddleware(settings)))
	storefrontv1.RegisterStoreContentServiceHTTPServer(srv, languageContentStub{})
	request := func(header, query, want string) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/storefront/posts"+query, nil)
		req.Header.Set("Accept-Language", header)
		recorder := httptest.NewRecorder()
		srv.ServeHTTP(recorder, req)
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"title":"`+want+`"`) {
			t.Fatalf("header=%q query=%q: status=%d body=%s", header, query, recorder.Code, recorder.Body.String())
		}
	}
	request("", "", "en")
	request("zh-CN", "", "zh_CN")
	request("en-AU", "", "en")
	request("zh-CN", "?locale=en_US", "en")
	request("zh-CN", "?locale=fr", "en")
	settings["i18n.default_locale"] = json.RawMessage(`"zh_CN"`)
	settings["i18n.enabled_locales"] = json.RawMessage(`["zh_CN"]`)
	request("en-AU", "", "zh_CN")
	request("en", "?locale=en", "zh_CN")
}
