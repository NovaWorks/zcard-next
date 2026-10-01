package sanitize

import (
	"html"
	"regexp"
	"strings"
)

var smsLegacyHeading = regexp.MustCompile(`(?im)(<h[1-6][^>]*>|^#{1,6}\s+)[^<>\n]*[·・]\s*接码服务`)

// SMSPublicContent removes legacy automatically generated channel titles. Custom
// public product names and service instructions remain editable independently.
func SMSPublicContent(kind, name, description string) (string, string) {
	if kind != "sms_channel" {
		return name, description
	}
	old := name
	if strings.HasSuffix(strings.TrimSpace(name), "· 接码服务") {
		name = "接码服务"
		description = strings.ReplaceAll(description, old, name)
		description = strings.ReplaceAll(description, html.EscapeString(old), name)
	}
	description = smsLegacyHeading.ReplaceAllString(description, "${1}接码服务")
	return name, description
}
