package supply

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
	"golang.org/x/net/html"
)

func descriptionImageURLs(input string) []string {
	var out []string
	z := html.NewTokenizer(strings.NewReader(input))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			return out
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		if token.Data == "img" {
			for _, attr := range token.Attr {
				if attr.Key == "src" {
					out = append(out, attr.Val)
				}
			}
		}
	}
}

func TestDescriptionForHarvestsImagesAndLazySources(t *testing.T) {
	temporaryMediaStorage(t)
	var requested []string
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.String())
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	})
	svc := &SyncService{log: slog.Default()}
	conn := &ent.SupplyConnection{BaseURL: "https://8.8.8.8/api/supply", Settings: map[string]any{"cover_dir": "details"}}
	input := `<p class="text">说明 &amp; 正文</p><a href="/help">帮助</a>` +
		`<img src="/uploads/a.png" alt="202605261520404797593.png">` +
		`<img data-src="/uploads/a.png">` +
		`<img src="data:image/gif;base64,AA" data-original="../b.png">` +
		`<img src="/loading.gif" data-lazy-src="//1.1.1.1/c.png">` +
		`<img src="/actual.png" data-src="/ignored.png">` +
		`<img src="/loading.png"><img data-src="/lazy.png">`
	got := svc.descriptionFor(context.Background(), nil, conn, input)
	if !strings.HasPrefix(got, `<p class="text">说明 &amp; 正文</p><a href="/help">帮助</a>`) || !strings.Contains(got, `alt="202605261520404797593.png"`) {
		t.Fatalf("surrounding content or image alt was lost: %s", got)
	}
	want := []string{"https://8.8.8.8/uploads/a.png", "https://8.8.8.8/api/b.png", "https://1.1.1.1/c.png", "https://8.8.8.8/actual.png", "https://8.8.8.8/loading.png", "https://8.8.8.8/lazy.png"}
	if fmt.Sprint(requested) != fmt.Sprint(want) {
		t.Fatalf("requested=%v want=%v", requested, want)
	}
	urls := descriptionImageURLs(sanitize.RichHTML(got))
	if len(urls) != 7 || urls[0] != urls[1] {
		t.Fatalf("image sources did not survive sanitization or duplicate was not reused: %v", urls)
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, "/uploads/details/") {
			t.Fatalf("inline image was not localized: %q", u)
		}
		if _, err := os.Stat(media.StorageRoot + "/" + strings.TrimPrefix(u, "/uploads/")); err != nil {
			t.Fatalf("inline image file missing: %v", err)
		}
	}
}

func TestDescriptionForLimitsDownloadsAndResolvesFallbacks(t *testing.T) {
	temporaryMediaStorage(t)
	var hits int
	mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
	})
	svc := &SyncService{log: slog.Default()}
	conn := &ent.SupplyConnection{BaseURL: "https://8.8.8.8", Settings: map[string]any{"cover_dir": "limits"}}
	var input strings.Builder
	for i := 0; i < maxDescriptionImages+3; i++ {
		fmt.Fprintf(&input, `<img src="/image-%d.png"><img src="/image-%d.png">`, i, i)
	}
	got := svc.descriptionFor(context.Background(), nil, conn, input.String())
	urls := descriptionImageURLs(got)
	if hits != maxDescriptionImages || len(urls) != (maxDescriptionImages+3)*2 {
		t.Fatalf("download budget or image preservation failed: hits=%d sources=%d", hits, len(urls))
	}
	for i, u := range urls {
		if u != fmt.Sprintf("https://8.8.8.8/image-%d.png", i/2) {
			t.Fatalf("fallback remained relative: %q", u)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = svc.descriptionFor(ctx, nil, conn, `<p>正文</p><img src="/canceled.png">`)
	if hits != maxDescriptionImages || !strings.Contains(got, `src="https://8.8.8.8/canceled.png"`) {
		t.Fatalf("canceled content did not retain absolute fallback: %s", got)
	}
}

func TestDescriptionForRejectsUnsafeSources(t *testing.T) {
	svc := &SyncService{log: slog.Default()}
	conn := &ent.SupplyConnection{BaseURL: "https://8.8.8.8"}
	input := `<p>保留正文</p><img src="javascript:alert(1)"><img src="file:///etc/passwd"><img src="data:image/png;base64,AA"><img src="https://user:secret@8.8.8.8/a.png">`
	got := svc.descriptionFor(context.Background(), nil, conn, input)
	if len(descriptionImageURLs(got)) != 0 || !strings.Contains(got, `<p>保留正文</p>`) {
		t.Fatalf("unsafe image source retained or content lost: %s", got)
	}
}

func TestDescriptionForPreservesProtectedOrLockedContent(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(fmt.Sprintf("description_protected_%t", protected), func(t *testing.T) {
			repo, d := newTestRepo(t)
			local := d.Client.Product.Create().SetName("local").SetSlug("local").SetDescription(`<img src="/uploads/manual.png">本地介绍`).SetDescriptionProtected(protected).SetIsLocked(!protected).SaveX(context.Background())
			svc := &SyncService{repo: repo, log: slog.Default()}
			conn := &ent.SupplyConnection{BaseURL: "https://8.8.8.8"}
			mockMediaClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("protected description fetched an upstream image")
			})
			got := svc.descriptionFor(context.Background(), &ent.SupplyMapping{LocalProductID: local.ID}, conn, `<img src="/upstream.png">`)
			if got != local.Description || len(svc.coverDirs) != 0 {
				t.Fatalf("protected description changed or allocated media: %q", got)
			}
		})
	}
}
