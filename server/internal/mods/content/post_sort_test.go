package content

import (
	"context"
	"reflect"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPostSort(t *testing.T) {
	ctx := context.Background()
	r := newContentRepo(t)
	firstSort, lastSort := int32(1), int32(20)
	create := func(slug, typ string, sort *int32, published bool) *ent.Post {
		p, err := r.CreatePost(ctx, PostInput{Slug: slug, Type: typ, TitleJSON: `{"zh_CN":"标题"}`, ContentJSON: `{"zh_CN":"正文"}`, Sort: sort, IsPublished: published})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	last := create("last", "notice", &lastSort, true)
	first := create("first", "notice", &firstSort, true)
	defaultPost := create("default", "notice", nil, true)
	_ = create("draft", "notice", nil, false)
	_ = create("blog", "blog", nil, true)
	assertIDs := func(rows []*ent.Post, want ...uint64) {
		t.Helper()
		ids := make([]uint64, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("order %v, want %v", ids, want)
		}
	}
	rows, total, err := r.ListPublishedPosts(ctx, "notice", 0, 1, 2)
	if err != nil || total != 3 {
		t.Fatalf("list: total=%d err=%v", total, err)
	}
	assertIDs(rows, defaultPost.ID, first.ID)
	rows, _, err = r.ListPublishedPosts(ctx, "notice", 0, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(rows, last.ID)

	// A legacy title-only update must not reset sorting; explicit zero must reset it.
	var req adminv1.UpdatePostRequest
	if err := protojson.Unmarshal([]byte(`{"title_json":"{\"zh_CN\":\"新标题\"}"}`), &req); err != nil {
		t.Fatal(err)
	}
	updated, err := r.UpdatePost(ctx, last.ID, PostInput{TitleJSON: req.TitleJson, Sort: req.Sort})
	if err != nil || updated.Sort != lastSort {
		t.Fatalf("omitted sort changed: %v %v", updated, err)
	}
	if err := protojson.Unmarshal([]byte(`{"sort":0}`), &req); err != nil {
		t.Fatal(err)
	}
	updated, err = r.UpdatePost(ctx, last.ID, PostInput{Sort: req.Sort})
	if err != nil || updated.Sort != 0 || toPostPB(updated).Sort != 0 {
		t.Fatalf("zero reset failed: %v %v", updated, err)
	}
	rows, _, err = r.ListPosts(ctx, "notice", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if rows[len(rows)-1].ID != first.ID {
		t.Fatal("admin order ignored sort")
	}
	negative := int32(-1)
	if _, err = r.UpdatePost(ctx, first.ID, PostInput{Sort: &negative}); err == nil {
		t.Fatal("negative sort accepted")
	}
}
