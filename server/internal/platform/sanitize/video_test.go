package sanitize

import (
	"strings"
	"testing"
)

func TestRichVideoPolicy(t *testing.T) {
	input := `<video data-w-e-type="video" controls playsinline preload="none" src="https://cdn.example/video.mp4" poster="/uploads/cover.png" autoplay onerror="alert(1)"></video><iframe src="https://example.com"></iframe>`
	got := RichHTML(input)
	for _, want := range []string{"<video", `src="https://cdn.example/video.mp4"`, `poster="/uploads/cover.png"`, "controls", "playsinline"} {
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
	for _, bad := range []string{"autoplay", "onerror", "iframe"} {
		if strings.Contains(got, bad) {
			t.Fatal(got)
		}
	}
	if strings.Contains(HTML(input), "<video") {
		t.Fatal("video allowed in ordinary user content")
	}
	for _, url := range []string{"javascript:alert(1)", "data:video/mp4;base64,AAAA", "//evil.example/a.mp4", "http://example.com/a.mp4"} {
		if strings.Contains(RichHTML(`<video src="`+url+`"></video>`), `src=`) {
			t.Fatal(url)
		}
	}
}

func TestWangVideoKeepsSourceAndDisablesEagerLoading(t *testing.T) {
	input := `<div data-w-e-type="video" data-w-e-is-void><video poster="/uploads/cover.png" controls="true" width="auto" height="auto" preload="auto"><source src="/uploads/tutorial.mp4" type="video/mp4"/></video></div>`
	got := RichHTML(input)
	for _, want := range []string{`src="/uploads/tutorial.mp4"`, `poster="/uploads/cover.png"`, `preload="none"`, "controls", "playsinline"} {
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
	if got != RichHTML(got) {
		t.Fatal("video normalization is not stable", got, RichHTML(got))
	}
}
