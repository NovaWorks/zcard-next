package supply

// 封面采集测试：相对路径拼接 / 下载落盘（临时存储根）/ 失败 fail-open /
// 去重（同 URL 只请求一次）。

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
)

func TestResolveUpstreamURL(t *testing.T) {
	cases := []struct{ base, cover, want string }{
		{"https://tghao.uk", "/assets/cache/images/a.jpg", "https://tghao.uk/assets/cache/images/a.jpg"},
		{"https://tghao.uk/", "/assets/a.jpg", "https://tghao.uk/assets/a.jpg"},
		{"https://tghao.uk", "https://cdn.x.com/b.jpg", "https://cdn.x.com/b.jpg"},
		{"https://tghao.uk", "", ""},
		{"https://tghao.uk", "assets/a.jpg", "https://tghao.uk/assets/a.jpg"},
	}
	for _, c := range cases {
		if got := resolveUpstreamURL(c.base, c.cover); got != c.want {
			t.Fatalf("resolve(%q, %q) = %q, want %q", c.base, c.cover, got, c.want)
		}
	}
}

func TestDownloadCover(t *testing.T) {
	// 临时存储根
	media.StorageRoot = t.TempDir()
	defer func() { media.StorageRoot = "data/uploads" }()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3})
		case "/ok-relative.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 1, 2, 3})
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/bad-type":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html></html>"))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()

	svc := &SyncService{log: slog.Default()}
	baseURL := srv.URL
	ctx := context.Background()

	// 1) 完整 URL 成功 → 本地 /uploads/<渠道目录>/ 路径 + 文件存在
	got := svc.downloadCover(ctx, baseURL, "/ok.png", "渠道A")
	if !strings.HasPrefix(got, "/uploads/") {
		t.Fatalf("成功下载应返回本地路径: %q", got)
	}
	rel := strings.TrimPrefix(got, "/uploads/")
	if _, err := os.Stat(media.StorageRoot + "/" + rel); err != nil {
		t.Fatalf("本地文件不存在: %v", err)
	}

	// 2) 相对路径 → 拼接完整 URL 下载成功（目录沿用）
	got2 := svc.downloadCover(ctx, baseURL, "ok-relative.jpg", "渠道A")
	if !strings.HasPrefix(got2, "/uploads/") {
		t.Fatalf("相对路径下载应成功: %q", got2)
	}

	// 3) 失败 fail-open：404 → 完整上游 URL
	got3 := svc.downloadCover(ctx, baseURL, "/missing", "渠道A")
	if got3 != srv.URL+"/missing" {
		t.Fatalf("404 应 fail-open 返回完整 URL: %q", got3)
	}

	// 4) 非图片类型 → fail-open
	got4 := svc.downloadCover(ctx, baseURL, "/bad-type", "渠道A")
	if got4 != srv.URL+"/bad-type" {
		t.Fatalf("非图片应 fail-open: %q", got4)
	}

	// 5) 空封面 → 空
	if got5 := svc.downloadCover(ctx, baseURL, "", "渠道A"); got5 != "" {
		t.Fatalf("空封面应返回空: %q", got5)
	}

	// 6) 去重：同 URL 再请求一次 → 命中缓存，不新增请求
	before := hits.Load()
	_ = svc.downloadCover(ctx, baseURL, "/ok.png", "渠道A")
	if hits.Load() != before {
		t.Fatal("同 URL 应命中去重缓存（不重复请求）")
	}
}

func TestAllocateCoverDir(t *testing.T) {
	// 无占用 → 原名字
	if got := allocateCoverDir(nil, "渠道A"); got != "渠道A" {
		t.Fatalf("空闲名应原样: %q", got)
	}
	// 占用 → 加 2/3
	existing := map[string]bool{"渠道A": true, "渠道A2": true}
	if got := allocateCoverDir(existing, "渠道A"); got != "渠道A3" {
		t.Fatalf("重名应递增: %q", got)
	}
	// 空名 → 空（年月目录兜底）
	if got := allocateCoverDir(nil, ""); got != "" {
		t.Fatalf("空名应返回空: %q", got)
	}
	// 净化
	if got := sanitizeSubDir("TG 渠道/官方!1"); got != "TG-渠道-官方-1" {
		t.Fatalf("净化结果: %q", got)
	}
}

func TestDeleteLocalCover(t *testing.T) {
	media.StorageRoot = t.TempDir()
	defer func() { media.StorageRoot = "data/uploads" }()
	// 存一张图
	rel, err := media.SaveLocalIn("渠道B", []byte{0x89, 0x50, 0x4e, 0x47, 1}, ".png")
	if err != nil {
		t.Fatal(err)
	}
	full := media.StorageRoot + "/" + rel
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	// 删除（/uploads/ 前缀）
	deleteLocalCover("/uploads/" + rel)
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatal("本地封面应已删除")
	}
	// 非本地路径不动
	deleteLocalCover("https://tghao.uk/x.jpg") // 不应 panic
}
