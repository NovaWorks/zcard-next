package sanitize

import (
	"strings"
	"testing"
)

// TestWangEditorOutputPass 白名单对拍：wangEditor 5 典型输出结构入库清洗后关键语义保留。
func TestWangEditorOutputPass(t *testing.T) {
	// wangEditor 典型产物：标题/加粗组/列表/引用/表格/居中段/图片（素材库相对路径）
	in := `<h2>商品说明</h2><p><strong>自动发货</strong><em>秒发</em><u>稳定</u><s>原价</s></p>` +
		`<ul><li>支持 API</li><li>支持批发</li></ul>` +
		`<blockquote>官方授权</blockquote>` +
		`<table><tbody><tr><th colspan="2">规格</th></tr><tr><td>月卡</td><td>30 天</td></tr></tbody></table>` +
		`<p><img src="/uploads/2026/08/a.png" alt="" width="400"></p>` +
		`<p style="text-align:center">居中说明</p>` +
		`<script>alert(1)</script><img src="x" onerror="hack()">`
	out := HTML(in)

	for _, keep := range []string{
		"<h2>", "<strong>", "<em>", "<u>", "<s>", "<ul>", "<li>", "<blockquote>",
		"<table", "colspan", `<img src="/uploads/2026/08/a.png"`, "width",
	} {
		if !strings.Contains(out, keep) {
			t.Fatalf("白名单误杀富文本结构 %q:\n%s", keep, out)
		}
	}
	for _, strip := range []string{"<script", "onerror", "alert(1)"} {
		if strings.Contains(out, strip) {
			t.Fatalf("XSS 载荷未剥离 %q:\n%s", strip, out)
		}
	}
	// style 属性不在白名单（text-align 丢但不破坏内容）
	if strings.Contains(out, "text-align") {
		t.Fatalf("style 属性应剥离: %s", out)
	}
}
