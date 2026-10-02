package supply

// 封面采集测试：相对路径拼接 / 下载落盘（临时存储根）/ 失败 fail-open /
// 去重（同 URL 只请求一次）。

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
)

type mediaRoundTripper func(*http.Request) (*http.Response, error)

func (f mediaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise production URL/redirect checks with a public URL, while serving the
// image bytes in memory. Tests never enable the private-address escape hatch.
func mockMediaClient(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	old := coverClient
	client := httpx.NewSafeClient(10 * time.Second)
	client.Transport = mediaRoundTripper(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler(w, r)
		resp := w.Result()
		resp.Request = r
		return resp, nil
	})
	coverClient = client
	t.Cleanup(func() { coverClient = old })
}

func temporaryMediaStorage(t *testing.T) {
	t.Helper()
	old := media.StorageRoot
	media.StorageRoot = t.TempDir()
	t.Cleanup(func() { media.StorageRoot = old })
}

func TestResolveUpstreamURL(t *testing.T) {
	cases := []struct{ base, cover, want string }{
		{"https://tghao.uk", "/assets/cache/images/a.jpg", "https://tghao.uk/assets/cache/images/a.jpg"},
		{"https://tghao.uk/", "/assets/a.jpg", "https://tghao.uk/assets/a.jpg"},
		{"https://tghao.uk", "https://cdn.x.com/b.jpg", "https://cdn.x.com/b.jpg"},
		{"https://tghao.uk", "", ""},
		{"https://tghao.uk", "assets/a.jpg", "https://tghao.uk/assets/a.jpg"},
		{"https://tghao.uk/api/supply", "assets/a.jpg", "https://tghao.uk/api/supply/assets/a.jpg"},
		{"https://tghao.uk/api/supply", "/assets/a.jpg", "https://tghao.uk/assets/a.jpg"},
		{"https://tghao.uk/store%2Fa", "assets/a.jpg", "https://tghao.uk/store%2Fa/assets/a.jpg"},
		{"https://tghao.uk/api/supply/", "../assets/a.jpg", "https://tghao.uk/api/assets/a.jpg"},
		{"https://tghao.uk/api/supply?key=secret", "//cdn.example.com/a.png", "https://cdn.example.com/a.png"},
		{"https://tghao.uk", " HTTPS://cdn.example.com/a.png ", "https://cdn.example.com/a.png"},
		{"https://tghao.uk", "javascript:alert(1)", ""},
		{"https://tghao.uk", "data:image/png;base64,abc", ""},
		{"https://tghao.uk", "file:///etc/passwd", ""},
		{"https://tghao.uk", "https://user:secret@cdn.example.com/a.png", ""},
		{"https://user:secret@tghao.uk", "/a.png", ""},
		{"https://tghao.uk", `\\cdn.example.com\a.png`, ""},
	}
	for _, c := range cases {
		if got := resolveUpstreamURL(c.base, c.cover); got != c.want {
			t.Fatalf("resolve(%q, %q) = %q, want %q", c.base, c.cover, got, c.want)
		}
	}
}

func TestDownloadCover(t *testing.T) {
	// 临时存储根
	temporaryMediaStorage(t)

	var hits atomic.Int32
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
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
	})

	svc := &SyncService{log: slog.Default()}
	baseURL := "https://8.8.8.8"
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
	if got3 != baseURL+"/missing" {
		t.Fatalf("404 应 fail-open 返回完整 URL: %q", got3)
	}

	// 4) 非图片类型 → fail-open
	got4 := svc.downloadCover(ctx, baseURL, "/bad-type", "渠道A")
	if got4 != baseURL+"/bad-type" {
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

func TestDownloadCoverRetriesFailuresAndSeparatesOrigins(t *testing.T) {
	temporaryMediaStorage(t)
	var hits int
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("User-Agent") != httpx.UserAgent || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected media headers: %v", r.Header)
		}
		if r.Header.Get("Referer") != "https://"+r.URL.Host+"/" {
			t.Errorf("referer should contain only the upstream origin: %q", r.Header.Get("Referer"))
		}
		if hits == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	})
	svc := &SyncService{log: slog.Default()}
	ctx := context.Background()
	base := "https://8.8.8.8/api?secret=hidden"
	if got := svc.downloadCover(ctx, base, "/retry.png", "same"); got != "https://8.8.8.8/retry.png" {
		t.Fatalf("failure fallback: %q", got)
	}
	first := svc.downloadCover(ctx, base, "/retry.png", "same")
	if !strings.HasPrefix(first, "/uploads/") || hits != 2 {
		t.Fatalf("failure was cached: %q, requests=%d", first, hits)
	}
	if got := svc.downloadCover(ctx, "https://8.8.8.8", "retry.png", "same"); got != first || hits != 2 {
		t.Fatalf("equivalent resolved URL did not reuse the image: %q, requests=%d", got, hits)
	}
	second := svc.downloadCover(ctx, "https://1.1.1.1", "/retry.png", "same")
	if !strings.HasPrefix(second, "/uploads/") || first == second || hits != 3 {
		t.Fatalf("different upstream images were merged: first=%q second=%q requests=%d", first, second, hits)
	}
}

func TestDownloadCoverBlocksPrivateAddressesAndRedirects(t *testing.T) {
	temporaryMediaStorage(t)
	var hits int
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Location", "http://127.0.0.1/private.png")
		w.WriteHeader(http.StatusFound)
	})
	svc := &SyncService{log: slog.Default()}
	for _, target := range []string{"http://127.0.0.1/private.png", "http://169.254.169.254/metadata.png", "http://[::1]/private.png"} {
		if got := svc.downloadCover(context.Background(), "https://8.8.8.8", target, "test"); got != target {
			t.Fatalf("blocked address fallback changed: %q", got)
		}
	}
	if hits != 0 {
		t.Fatal("a private media URL reached the transport")
	}
	got := svc.downloadCover(context.Background(), "https://8.8.8.8", "/redirect.png", "test")
	if got != "https://8.8.8.8/redirect.png" || hits != 1 {
		t.Fatalf("private redirect was followed: %q requests=%d", got, hits)
	}
}

func TestDownloadCoverLogsDoNotExposeImageQuery(t *testing.T) {
	var logs bytes.Buffer
	svc := &SyncService{log: slog.New(slog.NewTextHandler(&logs, nil))}
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	src := "https://8.8.8.8/image.png?signature=secret-token"
	if got := svc.downloadCover(context.Background(), "https://8.8.8.8", src, "test"); got != src {
		t.Fatalf("usable signed fallback URL was altered: %q", got)
	}
	if strings.Contains(logs.String(), "secret-token") || strings.Contains(logs.String(), "signature") {
		t.Fatalf("image query was logged: %s", logs.String())
	}
	wantErr := errors.New("connection failed")
	if got := mediaFetchError(&url.Error{Op: "Get", URL: src, Err: wantErr}); got != wantErr {
		t.Fatalf("HTTP error exposed request URL: %v", got)
	}
}

func TestDownloadCoverRetriesHTMLAtImageURL(t *testing.T) {
	temporaryMediaStorage(t)
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var hits int
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>Login required</html>"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload.Bytes())
	})
	svc := &SyncService{log: slog.Default()}
	src := "https://8.8.8.8/blocked.png?signature=fixture"
	if got := svc.downloadCover(context.Background(), "https://8.8.8.8", src, "retry"); got != src {
		t.Fatalf("HTML page was saved as a local image: %q", got)
	}
	if len(svc.coverCache) != 0 {
		t.Fatal("HTML response was cached as a successful download")
	}
	got := svc.downloadCover(context.Background(), "https://8.8.8.8", src, "retry")
	if !strings.HasPrefix(got, "/uploads/") || hits != 2 {
		t.Fatalf("real PNG could not replace the earlier HTML failure: %q, requests=%d", got, hits)
	}
	body, err := os.ReadFile(media.StorageRoot + "/" + strings.TrimPrefix(got, "/uploads/"))
	if err != nil || !bytes.Equal(body, payload.Bytes()) {
		t.Fatalf("saved image was not the successful PNG: %v", err)
	}
	if cached := svc.downloadCover(context.Background(), "https://8.8.8.8", src, "retry"); cached != got || hits != 2 {
		t.Fatalf("successful retry did not become cached: %q requests=%d", cached, hits)
	}
}

func TestCoverExtRejectsUnsupportedExplicitMIME(t *testing.T) {
	for _, mime := range []string{"text/html", "text/plain", "application/json", "image/svg+xml", "image/avif"} {
		if ext := coverExt(mime, "https://8.8.8.8/misleading.png"); ext != "" {
			t.Fatalf("unsupported MIME %q was saved under filename extension %q", mime, ext)
		}
	}
	for _, mime := range []string{"", "application/octet-stream", "binary/octet-stream; charset=binary"} {
		if ext := coverExt(mime, "https://8.8.8.8/image.png"); ext != ".png" {
			t.Fatalf("generic MIME %q did not allow PNG filename fallback: %q", mime, ext)
		}
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
