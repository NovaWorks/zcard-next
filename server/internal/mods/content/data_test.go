package content

// P2-04 必测项：banner 时间窗三态、sanitize XSS 剥离、slug 唯一、
// 发布状态机（published_at 首发回填）、多语言回落、移动端图回落。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newContentRepo(t *testing.T) *ContentRepo {
	t.Helper()
	return NewContentRepo(newTestData(t), nil)
}

func TestBannerTimeWindow(t *testing.T) {
	r := newContentRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	must := func(in BannerInput) uint64 {
		b, err := r.CreateBanner(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return b.ID
	}
	// 进行中 / 未开始 / 已结束 / 永久（无窗口）
	active := must(BannerInput{Name: "进行中", Position: "top", Image: "a.png", LinkType: "url", IsActive: true,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour)})
	future := must(BannerInput{Name: "未开始", Position: "top", Image: "b.png", LinkType: "url", IsActive: true,
		StartAt: now.Add(time.Hour)})
	expired := must(BannerInput{Name: "已结束", Position: "top", Image: "c.png", LinkType: "url", IsActive: true,
		EndAt: now.Add(-time.Hour)})
	_ = must(BannerInput{Name: "永久", Position: "top", Image: "d.png", LinkType: "url", IsActive: true})
	_ = future
	_ = expired

	got, err := r.ListActiveBanners(ctx, "top")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, b := range got {
		names[b.Name] = true
	}
	if !names["进行中"] || !names["永久"] {
		t.Fatalf("进行中/永久应可见: %v", names)
	}
	if names["未开始"] || names["已结束"] {
		t.Fatalf("未开始/已结束应过滤: %v", names)
	}
	_ = active
}

func TestPostSanitizeAndSlug(t *testing.T) {
	r := newContentRepo(t)
	ctx := context.Background()

	// XSS 剥离：script/onclick 不入库
	contentJSON := `{"zh_CN": "<p>正常</p><script>alert(1)</script><img src=x onerror=alert(2)>"}`
	p, err := r.CreatePost(ctx, PostInput{
		Slug: "hello", Type: "blog",
		TitleJSON:   `{"zh_CN": "标题"}`,
		ContentJSON: contentJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(p.ContentJSON), &m); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m["zh_CN"], "script") || strings.Contains(m["zh_CN"], "onerror") {
		t.Fatalf("XSS 未剥离: %s", m["zh_CN"])
	}
	if !strings.Contains(m["zh_CN"], "<p>正常</p>") {
		t.Fatalf("白名单标签被误杀: %s", m["zh_CN"])
	}

	// slug 唯一
	if _, err := r.CreatePost(ctx, PostInput{
		Slug: "hello", Type: "blog",
		TitleJSON: `{"zh_CN": "重复"}`, ContentJSON: `{"zh_CN": "x"}`,
	}); err != ErrSlugDuplicated {
		t.Fatalf("slug 重复应拒绝: %v", err)
	}
}

func TestPublishStateMachine(t *testing.T) {
	r := newContentRepo(t)
	ctx := context.Background()

	p, err := r.CreatePost(ctx, PostInput{
		Slug: "pub", Type: "notice",
		TitleJSON: `{"zh_CN": "公告"}`, ContentJSON: `{"zh_CN": "内容"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.IsPublished {
		t.Fatal("草稿初始应为未发布")
	}
	// 未发布 → 前台不可见
	if _, err := r.GetPublishedBySlug(ctx, "pub"); err != ErrNotFound {
		t.Fatalf("草稿应 404: %v", err)
	}
	// 首发回填 published_at
	p1, err := r.SetPublished(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p1.IsPublished || p1.PublishedAt.IsZero() {
		t.Fatalf("发布状态错误: %+v", p1)
	}
	firstPub := p1.PublishedAt
	// 取消再发布：published_at 不覆盖
	_, _ = r.SetPublished(ctx, p.ID, false)
	time.Sleep(1100 * time.Millisecond) // 确保时间戳不同
	p2, _ := r.SetPublished(ctx, p.ID, true)
	if !p2.PublishedAt.Equal(firstPub) {
		t.Fatal("首发时间不应被覆盖")
	}
	// 已发布 → 前台可见
	got, err := r.GetPublishedBySlug(ctx, "pub")
	if err != nil || got.ID != p.ID {
		t.Fatalf("已发布应可见: %v", err)
	}
}

func TestCategoryDeleteGuard(t *testing.T) {
	r := newContentRepo(t)
	ctx := context.Background()

	c, err := r.CreateCategory(ctx, "公告分类", "notice-cat", 0)
	if err != nil {
		t.Fatal(err)
	}
	// 空分类可删
	if err := r.DeleteCategory(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	// 有文章的分类拒绝删除
	c2, _ := r.CreateCategory(ctx, "博客分类", "blog-cat", 0)
	if _, err := r.CreatePost(ctx, PostInput{
		Slug: "in-cat", Type: "blog", TitleJSON: `{"zh_CN": "t"}`,
		ContentJSON: `{"zh_CN": "c"}`, CategoryID: c2.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteCategory(ctx, c2.ID); err != ErrCategoryInUse {
		t.Fatalf("有文章分类删除应拒绝: %v", err)
	}
}

func TestLangFallback(t *testing.T) {
	// 多语言回落：请求语言 → zh_CN → 首个非空
	m := map[string]string{"zh_CN": "中文", "en": "English"}
	if got := LangValue(m, "en"); got != "English" {
		t.Fatalf("en 应命中: %s", got)
	}
	if got := LangValue(m, "ja"); got != "中文" {
		t.Fatalf("ja 应回落 zh_CN: %s", got)
	}
	only := map[string]string{"fr": "Français"}
	if got := LangValue(only, "ja"); got != "Français" {
		t.Fatalf("无 zh_CN 应回落首个非空: %s", got)
	}
	// 内容回落（JSON 字符串）
	if got := LangContent(`{"zh_CN":"<p>中</p>","en":"<p>EN</p>"}`, "en"); got != "<p>EN</p>" {
		t.Fatalf("内容回落错误: %s", got)
	}
}

// newTestData 内存 SQLite（同 data 包模式）。
func newTestData(t *testing.T) *data.Data {
	t.Helper()
	handle, err := db.SQLite.Open("file:contenttest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
}
