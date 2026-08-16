package media

// P3-06 必测项：伪装扩展名拒绝、超大小拒绝、重编码剥离附加载荷、
// 引用计数增减与删除门禁、静态服务安全（穿越/非白名单扩展）。

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	pngimg "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newMediaRepo(t *testing.T) (*MediaRepo, *data.Data) {
	t.Helper()
	// 存储根指向临时目录（不污染 data/uploads）
	root := t.TempDir()
	old := StorageRoot
	StorageRoot = root
	t.Cleanup(func() { StorageRoot = old })

	handle, err := db.SQLite.Open(fmt.Sprintf("file:mediatest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewMediaRepo(d), d
}

// tinyPNG 生成 2×2 测试图。
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestUploadSecurity 上传安全门禁。
func TestUploadSecurity(t *testing.T) {
	r, _ := newMediaRepo(t)
	ctx := context.Background()
	png := tinyPNG(t)

	t.Run("正常PNG", func(t *testing.T) {
		res, err := r.Upload(ctx, port.UploadInput{Name: "a.png", ContentType: "image/png", Data: png})
		if err != nil || res.ID == 0 || res.Width != 2 {
			t.Fatalf("正常上传失败: %v %+v", err, res)
		}
	})

	t.Run("伪装扩展名拒绝", func(t *testing.T) {
		// .php / .html 内容为图片——扩展名白名单拒绝
		if _, err := r.Upload(ctx, port.UploadInput{Name: "shell.php", ContentType: "image/png", Data: png}); err == nil {
			t.Fatal("php 扩展名必须拒绝")
		}
		if _, err := r.Upload(ctx, port.UploadInput{Name: "x.html", ContentType: "text/html", Data: []byte("<script>1</script>")}); err == nil {
			t.Fatal("html 必须拒绝")
		}
	})

	t.Run("魔数不符拒绝", func(t *testing.T) {
		// .png 扩展名但内容是文本
		if _, err := r.Upload(ctx, port.UploadInput{Name: "fake.png", ContentType: "image/png", Data: []byte("not an image at all")}); err == nil {
			t.Fatal("伪 PNG 必须拒绝")
		}
	})

	t.Run("MIME不符拒绝", func(t *testing.T) {
		if _, err := r.Upload(ctx, port.UploadInput{Name: "a.png", ContentType: "application/x-php", Data: png}); err == nil {
			t.Fatal("声明 MIME 与扩展名不符必须拒绝")
		}
	})

	t.Run("超大小拒绝", func(t *testing.T) {
		big := make([]byte, MaxSizeBytes+1)
		copy(big, png)
		if _, err := r.Upload(ctx, port.UploadInput{Name: "big.png", ContentType: "image/png", Data: big}); err == nil {
			t.Fatal("超 10MB 必须拒绝")
		}
	})
}

// TestReencodeStripsPayload 重编码剥离：PNG 后附加恶意字节不落盘。
func TestReencodeStripsPayload(t *testing.T) {
	r, _ := newMediaRepo(t)
	ctx := context.Background()
	png := tinyPNG(t)
	dirty := append(append([]byte{}, png...), []byte("<?php evil(); ?> PAYLOAD_TRAILER")...)

	res, err := r.Upload(ctx, port.UploadInput{Name: "dirty.png", ContentType: "image/png", Data: dirty})
	if err != nil {
		t.Fatal(err)
	}
	// 读回物理文件：不得包含附加载荷
	m, _ := r.GetMedia(ctx, res.ID)
	full := filepath.Join(StorageRoot, m.Path)
	saved, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte("PAYLOAD_TRAILER")) || bytes.Contains(saved, []byte("evil()")) {
		t.Fatal("重编码未剥离附加载荷")
	}
	// 且仍是合法 PNG（可解码）
	if _, err := pngimg.Decode(bytes.NewReader(saved)); err != nil {
		t.Fatalf("重编码后不可解码: %v", err)
	}
	// 尺寸保留
	if m.Width != 2 || m.Height != 2 {
		t.Fatalf("尺寸丢失: %dx%d", m.Width, m.Height)
	}
}

// TestReferenceCount 引用计数 + 删除门禁。
func TestReferenceCount(t *testing.T) {
	r, d := newMediaRepo(t)
	ctx := context.Background()
	res, err := r.Upload(ctx, port.UploadInput{Name: "ref.png", ContentType: "image/png", Data: tinyPNG(t)})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ID

	// +1 两次
	_ = r.AddRefs(ctx, []uint64{id})
	_ = r.AddRefs(ctx, []uint64{id})
	m, _ := r.GetMedia(ctx, id)
	if m.RefCount != 2 {
		t.Fatalf("引用计数错误: %d", m.RefCount)
	}
	// 删除拒绝（未 confirm）+ 引用清单
	_, refs, err := r.DeleteMedia(ctx, []uint64{id}, false)
	if err != ErrReferenced || len(refs) != 1 {
		t.Fatalf("被引用删除应拒绝: %v refs=%d", err, len(refs))
	}
	// confirm 删除：DB 行删 + 物理文件删
	n, _, err := r.DeleteMedia(ctx, []uint64{id}, true)
	if err != nil || n != 1 {
		t.Fatalf("confirm 删除失败: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(StorageRoot, res.Path[len("/uploads/"):])); !os.IsNotExist(err) {
		t.Fatal("物理文件未清除")
	}
	// 释放下限 0
	_ = r.AddRefs(ctx, []uint64{id}) // 行已删：无操作
	if err := r.ReleaseRefs(ctx, []uint64{id}); err != nil {
		t.Fatal(err)
	}
	_ = d
}

// TestStaticSafety 静态服务安全（穿越/非白名单扩展/ETag）。
func TestStaticSafety(t *testing.T) {
	r, _ := newMediaRepo(t)
	ctx := context.Background()
	res, err := r.Upload(ctx, port.UploadInput{Name: "s.png", ContentType: "image/png", Data: tinyPNG(t)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, res.Path, nil)
	serveStatic(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("正常访问失败: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("缺 ETag")
	}
	// ETag 命中 304
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, res.Path, nil)
	req2.Header.Set("If-None-Match", etag)
	serveStatic(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("ETag 应 304: %d", rec2.Code)
	}
	// 穿越拒绝
	rec3 := httptest.NewRecorder()
	serveStatic(rec3, httptest.NewRequest(http.MethodGet, "/uploads/../configs/config.yaml", nil))
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("穿越应 404: %d", rec3.Code)
	}
	// 非白名单扩展（即使文件存在）
	os.WriteFile(filepath.Join(StorageRoot, "evil.php"), []byte("<?php"), 0o644)
	rec4 := httptest.NewRecorder()
	serveStatic(rec4, httptest.NewRequest(http.MethodGet, "/uploads/evil.php", nil))
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("php 应 404: %d", rec4.Code)
	}
	// 目录列表禁用
	rec5 := httptest.NewRecorder()
	serveStatic(rec5, httptest.NewRequest(http.MethodGet, "/uploads/", nil))
	if rec5.Code != http.StatusNotFound {
		t.Fatalf("目录列表应 404: %d", rec5.Code)
	}
}

// TestCategories 分类生命周期。
func TestCategories(t *testing.T) {
	r, _ := newMediaRepo(t)
	ctx := context.Background()

	c, err := r.CreateCategory(ctx, "Banner 图", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 子分类
	sub, err := r.CreateCategory(ctx, "首页", c.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 名称非法
	if _, err := r.CreateCategory(ctx, "", 0, 0); err == nil {
		t.Fatal("空名应拒绝")
	}
	// 有子分类删除拒绝
	if err := r.DeleteCategory(ctx, c.ID); err != ErrHasChildren {
		t.Fatalf("有子分类应拒绝: %v", err)
	}
	// 环拒绝
	if err := r.MoveCategory(ctx, c.ID, sub.ID); err == nil {
		t.Fatal("环应拒绝")
	}
	// 空子分类可删
	if err := r.DeleteCategory(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
}
