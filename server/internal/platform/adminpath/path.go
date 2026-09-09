// Package adminpath validates the private admin SPA entry independently of API authentication.
package adminpath

import (
	"fmt"
	"regexp"
	"strings"
)

var segment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Normalize accepts a relative or absolute URL path; empty means the startup default.
func Normalize(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	p := strings.Trim(value, "/")
	if p == "" || len(p) > 128 {
		return "", fmt.Errorf("后台路径必须为 1–128 位字母、数字、短横线或下划线，可用 / 分隔")
	}
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if !segment.MatchString(part) {
			return "", fmt.Errorf("后台路径只能包含字母、数字、短横线、下划线及路径分隔符")
		}
	}
	switch strings.ToLower(parts[0]) {
	case "payment", "tickets", "withdraw", "points", "coupons", "api", "uploads", "health", "payments", "install", "templates", "assets", "product", "products", "category", "categories", "login", "register", "member", "cart", "checkout", "order", "orders", "post", "posts", "fetch", "supplier", "supplier-apply", "recharge", "affiliate", "reset-password", "forgot-password":
		return "", fmt.Errorf("此路径已被系统或前台使用，请换一个后台路径")
	}
	return "/" + p, nil
}
