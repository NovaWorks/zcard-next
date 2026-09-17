package media

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func videoRequest(t *testing.T, body []byte, name string) *http.Request {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	p, e := w.CreateFormFile("file", name)
	if e != nil {
		t.Fatal(e)
	}
	p.Write(body)
	w.Close()
	r := httptest.NewRequest("POST", "/api/v1/admin/media/video", &b)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

func TestVideoUploadAndRanges(t *testing.T) {
	repo, _ := newMediaRepo(t)
	svc := NewAdminMediaService(repo)
	raw, e := os.ReadFile("testdata/tutorial.mp4")
	if e != nil {
		t.Fatal(e)
	}
	// The custom multipart endpoint must execute authorization before parsing files.
	allowed := false
	srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, _ := transport.FromServerContext(ctx)
			if tr.Operation() != VideoUploadOperation {
				t.Fatal(tr.Operation())
			}
			if !allowed {
				return nil, errors.Unauthorized("AUTH", "denied")
			}
			return next(ctx, req)
		}
	}))
	svc.RegisterVideoUpload(srv)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, videoRequest(t, raw, "tutorial.mp4"))
	if rec.Code != 401 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	allowed = true
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, videoRequest(t, raw, "tutorial.mp4"))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	videos, count, e := repo.ListMedia(context.Background(), 0, "", 1, 10, "video")
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
	_, count, _ = repo.ListMedia(context.Background(), 0, "", 1, 10)
	if count != 0 {
		t.Fatal("video leaked into image picker")
	}
	r := httptest.NewRequest("GET", "/uploads/"+videos[0].Path, nil)
	r.Header.Set("Range", "bytes=0-31")
	rec = httptest.NewRecorder()
	serveStatic(rec, r)
	if rec.Code != 206 || rec.Body.Len() != 32 || rec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatal(rec.Code, rec.Header(), rec.Body.Len())
	}
	for _, bad := range [][]byte{[]byte("<script>alert(1)</script>"), raw[:len(raw)/2]} {
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, videoRequest(t, bad, "bad.mp4"))
		if rec.Code != 400 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	t.Setenv("ZCARD_VIDEO_MAX_MB", "1")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, videoRequest(t, make([]byte, 1024*1024+1), "big.mp4"))
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	filepath.WalkDir(StorageRoot, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name()[0] == '.' {
			t.Error("temporary upload leaked", path)
		}
		return err
	})
}
