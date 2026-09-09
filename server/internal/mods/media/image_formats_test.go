package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image/gif"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestImageFormatsUploadAndStaticHTTP(t *testing.T) {
	repo, _ := newMediaRepo(t)
	s := khttp.NewServer()
	adminv1.RegisterAdminMediaServiceHTTPServer(s, NewAdminMediaService(repo))
	RegisterStatic(s)
	cases := []struct{ file, mime, ext string }{
		{"still.jpg", "image/jpeg", ".jpg"}, {"still.png", "image/png", ".png"},
		{"still.bmp", "image/png", ".png"}, {"still.tiff", "image/png", ".png"},
		{"still.webp", "image/webp", ".webp"}, {"still.avif", "image/avif", ".avif"},
		{"still.heic", "image/heic", ".heic"},
		{"still.ico", "image/x-icon", ".ico"}, {"still.svg", "image/svg+xml", ".svg"},
		{"animated.gif", "image/gif", ".gif"}, {"animated.png", "image/png", ".png"},
		{"animated.webp", "image/webp", ".webp"}, {"animated-opaque.webp", "image/webp", ".webp"},
		{"animated-lossless.webp", "image/webp", ".webp"}, {"animated-fallback.png", "image/png", ".png"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			// Real browsers and downloads often supply a misleading filename/MIME.
			body, _ := json.Marshal(map[string]string{"name": "customer.jpg", "content_type": "image/jpeg", "data_base64": base64.StdEncoding.EncodeToString(raw)})
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/v1/admin/media/upload", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			s.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("upload: %d %s", w.Code, w.Body.String())
			}
			var item adminv1.MediaItem
			if err := protojson.Unmarshal(w.Body.Bytes(), &item); err != nil {
				t.Fatal(err)
			}
			if item.Mime != tc.mime || !strings.HasSuffix(item.Url, tc.ext) {
				t.Fatalf("incorrect canonical type: %+v", &item)
			}
			w = httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", item.Url, nil))
			if w.Code != 200 || w.Header().Get("Content-Type") != tc.mime {
				t.Fatal("saved image not served", w.Code, w.Header())
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing nosniff")
			}
			if tc.mime == "image/svg+xml" && !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("SVG not sandboxed")
			}
			if tc.file == "animated.gif" {
				before, err := gif.DecodeAll(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				after, err := gif.DecodeAll(bytes.NewReader(w.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				if len(after.Image) != 2 || after.LoopCount != before.LoopCount || !reflect.DeepEqual(after.Delay, before.Delay) || !reflect.DeepEqual(after.Disposal, before.Disposal) {
					t.Fatal("GIF timing or frame count changed")
				}
				for i := range before.Image {
					if !reflect.DeepEqual(before.Image[i].Pix, after.Image[i].Pix) {
						t.Fatal("GIF pixels changed")
					}
				}
			}
			if strings.HasPrefix(tc.file, "animated") && tc.ext != ".gif" && !bytes.Equal(raw, w.Body.Bytes()) {
				t.Fatal("animated container flattened or modified")
			}
			m, err := repo.GetMedia(context.Background(), item.Id)
			if err != nil || m.Size != int64(w.Body.Len()) {
				t.Fatal("stored metadata mismatch", err)
			}
		})
	}
}

func TestImageFilenameAndMIMECompatibility(t *testing.T) {
	png := tinyPNG(t)
	for _, name := range []string{"clipboard", "download.bin", "image.JFIF", "image.apng", "image.webp"} {
		if _, mime, _, _, err := ValidateAndReencode(name, "application/octet-stream", png); err != nil || mime != "image/png" {
			t.Fatal(name, mime, err)
		}
	}
	svg := []byte("\xef\xbb\xbf<?xml version=\"1.0\"?><svg xmlns=\"http://www.w3.org/2000/svg\"/>")
	if _, mime, _, _, err := ValidateAndReencode("logo.svg", "text/plain", svg); err != nil || mime != "image/svg+xml" {
		t.Fatal(mime, err)
	}
}

func TestOversizedUploadHTTPMessage(t *testing.T) {
	repo, _ := newMediaRepo(t)
	s := khttp.NewServer()
	adminv1.RegisterAdminMediaServiceHTTPServer(s, NewAdminMediaService(repo))
	// An oversized upload must return an actionable size error, even before
	// format validation (and must never save a partial file).
	body, err := json.Marshal(map[string]string{"name": "large.gif", "content_type": "image/gif", "data_base64": base64.StdEncoding.EncodeToString(make([]byte, MaxSizeBytes+1))})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/admin/media/upload", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var problem struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if w.Code != 400 || problem.Reason != "media.TOO_LARGE" || problem.Message != "图片超过 10MB 上限，请压缩后重试" {
		t.Fatalf("wrong size error: %d %s", w.Code, w.Body.String())
	}
	files, err := os.ReadDir(StorageRoot)
	if err != nil || len(files) != 0 {
		t.Fatal("oversized upload left a file", err)
	}
}

func TestRejectBrokenContainers(t *testing.T) {
	for _, file := range []string{"animated.gif", "animated.webp", "animated.png", "still.avif", "still.ico"} {
		raw, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := ValidateAndReencode(file, "image/unknown", raw[:len(raw)/2]); err == nil {
			t.Fatal("truncated accepted", file)
		}
	}
	for _, svg := range []string{`<?xml version="1.0"?><document/>`, `<html><svg/></html>`, `<svg><script>alert(1)</script></svg>`, `<svg onload="alert(1)"/>`} {
		if _, _, _, _, err := ValidateAndReencode("bad.svg", "image/svg+xml", []byte(svg)); err == nil {
			t.Fatal("non-image or active SVG accepted")
		}
	}
	raw, _ := os.ReadFile("testdata/still.avif")
	// A movie ftyp alone must not be misidentified as AVIF.
	fake := make([]byte, 16)
	binary.BigEndian.PutUint32(fake, 16)
	copy(fake[4:], "ftypisom")
	if _, _, ok := SniffImage(fake); ok {
		t.Fatal("MP4 accepted")
	}
	if _, _, _, _, err := ValidateAndReencode("header.avif", "image/avif", raw[:binary.BigEndian.Uint32(raw[:4])]); err == nil {
		t.Fatal("AVIF header without image accepted")
	}
}
func TestAnimationAndDimensionLimits(t *testing.T) {
	raw, _ := os.ReadFile("testdata/animated.gif")
	raw = append([]byte{}, raw...)
	binary.LittleEndian.PutUint16(raw[6:8], 65535)
	binary.LittleEndian.PutUint16(raw[8:10], 65535)
	if _, _, _, _, err := ValidateAndReencode("large.gif", "image/gif", raw); err != ErrImageDimensions {
		t.Fatal("large dimensions not rejected before decode", err)
	}
	raw, _ = os.ReadFile("testdata/animated.png")
	clean, _, _, _, err := ValidateAndReencode("animation.png", "image/png", append(raw, []byte("TRAILER")...))
	if err != nil || !bytes.Equal(clean, raw) {
		t.Fatal("APNG trailer not removed", err)
	}
}
