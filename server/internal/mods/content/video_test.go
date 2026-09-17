package content

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPostVideoReferencesAcrossLocalesAndEdits(t *testing.T) {
	r := newContentRepo(t)
	ctx := context.Background()
	m := r.data.Client.Media.Create().SetName("tutorial").SetPath("tutorial.mp4").SetMime("video/mp4").SetSize(10).SetSha256("test").SaveX(ctx)
	encode := func(v string) string {
		b, _ := json.Marshal(map[string]string{"zh_CN": v, "en_US": v})
		return string(b)
	}
	in := PostInput{Slug: "video", Type: "blog", TitleJSON: `{"zh_CN":"教程"}`, ContentJSON: encode(`<video src="/uploads/tutorial.mp4" controls></video>`)}
	p, e := r.CreatePost(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if n := r.data.Client.Media.GetX(ctx, m.ID).RefCount; n != 1 {
		t.Fatal(n)
	}
	if _, e = r.UpdatePost(ctx, p.ID, in); e != nil {
		t.Fatal(e)
	}
	if n := r.data.Client.Media.GetX(ctx, m.ID).RefCount; n != 1 {
		t.Fatal(n)
	}
	in.ContentJSON = encode(`<p>no video</p>`)
	if _, e = r.UpdatePost(ctx, p.ID, in); e != nil {
		t.Fatal(e)
	}
	if n := r.data.Client.Media.GetX(ctx, m.ID).RefCount; n != 0 {
		t.Fatal(n)
	}
	in.ContentJSON = encode(`<video src="/uploads/missing.mp4"></video>`)
	if _, e = r.UpdatePost(ctx, p.ID, in); e == nil {
		t.Fatal("missing media saved")
	}
	if got := r.data.Client.Post.GetX(ctx, p.ID).ContentJSON; got != encode(`<p>no video</p>`) {
		t.Fatal("failed update not rolled back", got)
	}
	in.ContentJSON = encode(`<video src="/uploads/tutorial.mp4" controls></video>`)
	if _, e = r.UpdatePost(ctx, p.ID, in); e != nil {
		t.Fatal(e)
	}
	if e = r.DeletePost(ctx, p.ID); e != nil {
		t.Fatal(e)
	}
	if n := r.data.Client.Media.GetX(ctx, m.ID).RefCount; n != 0 {
		t.Fatal(n)
	}
	if e = r.DeletePost(ctx, p.ID); e != ErrNotFound {
		t.Fatal(e)
	}

}
