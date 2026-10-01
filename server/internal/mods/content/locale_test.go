package content

import (
	"context"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestBannerCacheSeparatesLanguagesAndTenants(t *testing.T) {
	d := newTestData(t)
	ctx := context.Background()
	for _, tenant := range []uint64{0, 5} {
		name := "Main"
		if tenant != 0 {
			name = "Subsite"
		}
		d.Client.Banner.Create().SetSubsiteID(tenant).SetName(name).SetPosition("top").SetImage("a.png").SetIsActive(true).
			SetTitleJSON(map[string]string{"zh_CN": name + " 中文", "en": name + " English"}).SaveX(ctx)
	}
	s := NewStoreContentService(NewContentRepo(d, nil))
	for _, tc := range []struct {
		tenant uint64
		locale string
		want   string
	}{{0, "zh_CN", "Main 中文"}, {0, "en", "Main English"}, {5, "en", "Subsite English"}, {0, "en", "Main English"}} {
		ctx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: tc.tenant, IsMain: tc.tenant == 0})
		ctx = i18n.WithPolicy(ctx, i18n.NewPolicy("zh_CN", []string{"zh_CN", "en"}))
		for i := 0; i < 2; i++ {
			banners, err := s.ListBanners(ctx, &storefrontv1.ListBannersRequest{Position: "top", Locale: tc.locale})
			if err != nil || len(banners.GetBanners()) != 1 || banners.Banners[0].Title != tc.want {
				t.Fatalf("tenant %d locale %s: got %v error %v", tc.tenant, tc.locale, banners, err)
			}
		}
	}
}

func TestPostsUseConfiguredLocaleAndPreserveTenantIsolation(t *testing.T) {
	d := newTestData(t)
	ctx := context.Background()
	d.Client.Post.Create().SetSlug("guide").SetTitleJSON(map[string]string{"zh_CN": "中文", "en": "English"}).
		SetSummaryJSON(map[string]string{"zh_CN": "摘要", "en": "Summary"}).SetContentJSON(`{"zh_CN":"<p>中文</p>","en":"<p>English</p>"}`).SetIsPublished(true).SaveX(ctx)
	d.Client.Post.Create().SetSubsiteID(5).SetSlug("subsite-guide").SetTitleJSON(map[string]string{"en": "Private subsite"}).SetContentJSON(`{"en":"Private"}`).SetIsPublished(true).SaveX(ctx)
	s := NewStoreContentService(NewContentRepo(d, nil))
	ctx = i18n.WithPolicy(ctx, i18n.NewPolicy("en", []string{"zh_CN", "en"}))
	detail, err := s.GetPost(ctx, &storefrontv1.GetPostRequest{Slug: "guide"})
	if err != nil || detail.Post.Title != "English" || detail.Content != "<p>English</p>" {
		t.Fatalf("default English content missing: %v %v", detail, err)
	}
	list, err := s.ListPosts(ctx, &storefrontv1.ListPostsRequest{})
	if err != nil || len(list.Posts) != 1 || list.Posts[0].Title != "English" {
		t.Fatalf("list language or tenant filter incorrect: %v %v", list, err)
	}
	if _, err := s.GetPost(ctx, &storefrontv1.GetPostRequest{Slug: "subsite-guide"}); err != ErrNotFound {
		t.Fatalf("another tenant's post was visible: %v", err)
	}
	ctx = i18n.WithPolicy(ctx, i18n.NewPolicy("zh_CN", []string{"zh_CN"}))
	detail, err = s.GetPost(ctx, &storefrontv1.GetPostRequest{Slug: "guide", Locale: "en"})
	if err != nil || detail.Post.Title != "中文" {
		t.Fatalf("disabled English did not fall back: %v %v", detail, err)
	}
}
