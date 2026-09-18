package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Stock reads have their own short retry budget; order submission retains the
// connection's existing retry/idempotency policy.
type stockReadKey struct{}

func stockReadContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, stockReadKey{}, true)
}
func isStockRead(ctx context.Context) bool { v, _ := ctx.Value(stockReadKey{}).(bool); return v }

// ACG requires an explicit integer. Missing/null/blank is not zero or unlimited.
func parseStock(raw json.RawMessage) (int32, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(raw, &s); err != nil {
			return -2, fmt.Errorf("库存格式无效")
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n < -1 {
		return -2, fmt.Errorf("上游未返回有效库存")
	}
	return int32(n), nil
}

var stock404 = regexp.MustCompile(`(?i)404\s+not\s+found`)
var stockHTMLTags = regexp.MustCompile(`<[^>]*>`)
var stockBase64 = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={0,2}`)

// Only explicit route absence permits legacy fallback. Generic WAF/login HTML
// must retain the normal error/retry behavior. Never execute the embedded JS.
func stockRouteMissing(body []byte) bool {
	if len(body) > 256*1024 {
		return false
	}
	if stock404.MatchString(stockHTMLTags.ReplaceAllString(string(body), " ")) {
		return true
	}
	for _, token := range stockBase64.FindAll(body, 16) {
		decoded, err := base64.StdEncoding.DecodeString(string(token))
		if err == nil && stock404.MatchString(stockHTMLTags.ReplaceAllString(string(decoded), " ")) {
			return true
		}
	}
	return false
}
func missingStockRoute(err error) bool {
	var he *httpError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

// StockErrorSummary excludes upstream bodies/URLs/credentials from task/API output.
func StockErrorSummary(err error) string {
	if err == nil {
		return "库存未知"
	}
	var he *httpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "库存查询超时"
	case errors.Is(err, context.Canceled):
		return "库存查询已取消"
	case errors.As(err, &he):
		switch he.Status {
		case 401, 403:
			return "货源鉴权失败或访问被拒绝"
		case 404:
			return "货源库存接口不存在"
		case 429:
			return "货源限流或网关拦截"
		default:
			return fmt.Sprintf("货源 HTTP %d", he.Status)
		}
	case errors.Is(err, ErrRateLimited):
		return "货源限流或网关拦截"
	default:
		return "货源未返回有效库存（检查商品标识、权限及协议）"
	}
}
