package content

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"testing"
	"time"
)

func TestStorePostSEOMetadata(t *testing.T) {
	d := newTestData(t)
	ctx := context.Background()
	published := time.Unix(1755900000, 0)
	d.Client.Post.Create().SetSlug("guide").SetTitleJSON(map[string]string{"zh_CN": "使用说明"}).SetSummaryJSON(map[string]string{"zh_CN": "文章摘要"}).SetContentJSON(`{"zh_CN":"<p>正文</p>"}`).SetIsPublished(true).SetPublishedAt(published).SaveX(ctx)
	service := NewStoreContentService(NewContentRepo(d, nil))
	detail, err := service.GetPost(ctx, &storefrontv1.GetPostRequest{Slug: "guide"})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Post.Summary != "文章摘要" || detail.Post.PublishedAt != published.Unix() || detail.Content != "<p>正文</p>" {
		t.Fatalf("detail metadata missing: %v", detail)
	}
	list, err := service.ListPosts(ctx, &storefrontv1.ListPostsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Posts) != 1 || list.Posts[0].Summary != detail.Post.Summary || list.Posts[0].PublishedAt != detail.Post.PublishedAt {
		t.Fatal("list/detail metadata disagree")
	}
}
