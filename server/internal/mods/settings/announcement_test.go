package settings

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"
)

func TestAnnouncementMarkdown(t *testing.T) {
	source := "# 购买须知\n\n**自动发货**\n下一行\n\n- 第一项\n- 第二项\n\n[取货](/fetch)\n\n![图片](/uploads/demo.png)\n\n| 名称 | 说明 |\n| --- | --- |\n| 卡密 | 即时 |\n\n```go\nfmt.Println(1)\n```"
	rendered, summary := renderAnnouncement(source)
	for _, want := range []string{"<h1>购买须知</h1>", "<strong>自动发货</strong>", "<br>", "<ul>", `href="/fetch"`, `src="/uploads/demo.png"`, "<table>", "<pre><code"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in %s", want, rendered)
		}
	}
	if !strings.Contains(summary, "购买须知") || strings.ContainsAny(summary, "#*<>") {
		t.Fatalf("invalid summary: %q", summary)
	}
}

func TestAnnouncementRejectsActiveContent(t *testing.T) {
	for _, source := range []string{
		`<script>alert(1)</script><img src=x onerror=alert(2)>`,
		`[click](javascript:alert%281%29) ![x](data:image/svg+xml,test)`,
		`<iframe src="https://example.com"></iframe><svg onload=alert(1)>`,
	} {
		rendered, _ := renderAnnouncement(source)
		for _, bad := range []string{"<script", "onerror=", "javascript:", "data:", "<iframe", "<svg", "onload="} {
			if strings.Contains(rendered, bad) {
				t.Fatalf("unsafe content %q in %s", bad, rendered)
			}
		}
	}
}

func TestPublicAnnouncementPreservesSource(t *testing.T) {
	for _, source := range []string{"# 公告\n\n**内容**", "123", "true", "", "第一行\n第二行"} {
		raw, _ := json.Marshal(source)
		repo := &themeMemoryRepo{values: map[string]json.RawMessage{"ops.announcement": raw}}
		cfg, err := NewStorefrontConfigService(repo).GetPublicConfig(context.Background(), &emptypb.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		entries := map[string]string{}
		for _, entry := range cfg.Entries {
			if strings.HasPrefix(entry.Key, "ops.announcement") {
				var entriesValue string
				_ = json.Unmarshal([]byte(entry.ValueJson), &entriesValue)
				entries[entry.Key] = entriesValue
			}
		}
		if entries["ops.announcement"] != source || string(repo.values["ops.announcement"]) != string(raw) {
			t.Fatal("Markdown source was changed")
		}
		rendered, summary := renderAnnouncement(source)
		if entries["ops.announcement_html"] != rendered || entries["ops.announcement_summary"] != summary {
			t.Fatalf("derived content mismatch: %+v", entries)
		}
	}
}
