package sanitize

import (
	"strings"
	"testing"
)

func TestSMSPublicContent(t *testing.T) {
	for _, channel := range []string{"SmSCode", "Other provider", "A & B"} {
		old := channel + " · 接码服务"
		name, desc := SMSPublicContent("sms_channel", old, "<h1>"+old+"</h1><p>选择国家和服务</p>")
		if name != "接码服务" || strings.Contains(desc, channel) || !strings.Contains(desc, "选择国家和服务") {
			t.Fatalf("provider leaked: %s %s", name, desc)
		}
		_, desc = SMSPublicContent("sms_channel", "全球接码", "# "+old+"&#x20;\n选择国家和服务")
		if strings.Contains(desc, channel) {
			t.Fatal("old description heading leaked", desc)
		}
	}
	for _, kind := range []string{"card", "sms_channel"} {
		name, desc := SMSPublicContent(kind, "全球号码服务", "<p>服务说明</p>")
		if name != "全球号码服务" || desc != "<p>服务说明</p>" {
			t.Fatal("custom public content changed")
		}
	}
}
