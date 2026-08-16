package sanitize

import "testing"

func TestHTMLStripsScript(t *testing.T) {
	in := `<p>OK</p><script>alert("xss")</script><img src="x" onerror="alert(1)">`
	out := HTML(in)
	if containsAny(out, "script", "onerror", "alert") {
		t.Errorf("XSS 未剥离：%q", out)
	}
	if !containsAny(out, "<p>OK</p>") {
		t.Errorf("合法内容被误杀：%q", out)
	}
}

func TestHTMLStripsEventHandlers(t *testing.T) {
	in := `<div onclick="evil()" class="ok">text</div>`
	out := HTML(in)
	if containsAny(out, "onclick", "evil") {
		t.Errorf("事件属性未剥离：%q", out)
	}
	if !containsAny(out, "class=\"ok\"") {
		t.Errorf("白名单属性被误杀：%q", out)
	}
}

func TestHTMLStripsJavascriptURL(t *testing.T) {
	in := `<a href="javascript:alert(1)">link</a>`
	out := HTML(in)
	if containsAny(out, "javascript:") {
		t.Errorf("javascript: 协议未剥离：%q", out)
	}
}

func TestHTMLAllowsWhitelisted(t *testing.T) {
	in := `<h3>Title</h3><p>Text <b>bold</b> <a href="https://example.com" title="t">link</a></p><table><tr><td>cell</td></tr></table>`
	out := HTML(in)
	for _, want := range []string{"<h3>", "<b>", "<a href=", "<table>"} {
		if !containsAny(out, want) {
			t.Errorf("白名单标签 %q 被剥离：%q", want, out)
		}
	}
}

func TestTextStripsAll(t *testing.T) {
	out := Text(`<p>hello <b>world</b></p>`)
	if out != "hello world" {
		t.Errorf("纯文本应为 'hello world'，得到 %q", out)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) && (s == sub || len(s) > len(sub)) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
