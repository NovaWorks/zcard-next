package adapter

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// ImportFailure deliberately excludes upstream bodies, signed URLs and secrets.
// Authentication and access-denied are separate: a 403 may be a gateway policy.
type ImportFailure struct {
	Code, Summary string
	Retry, Pause  bool
	After         time.Duration
}

func ClassifyImportError(err error) ImportFailure {
	var he *httpError
	var ne net.Error
	switch {
	case errors.As(err, &he):
		switch {
		case he.Status == 401:
			return ImportFailure{Code: "AUTH_FAILED", Summary: "货源账号认证失败，请检查货源配置后继续", Pause: true}
		case he.Status == 403:
			return ImportFailure{Code: "ACCESS_DENIED", Summary: "货源拒绝访问，请检查账号权限或网站防护后继续", Pause: true}
		case he.Status == 429:
			return ImportFailure{Code: "RATE_LIMITED", Summary: "货源限流或网关拦截，正在等待重试", Retry: true, After: he.RetryAfter}
		case he.Status >= 500:
			return ImportFailure{Code: "UPSTREAM_UNAVAILABLE", Summary: "货源暂时不可用", Retry: true}
		case he.Status == http.StatusNotFound:
			return ImportFailure{Code: "NOT_FOUND", Summary: "商品或货源接口不存在"}
		default:
			return ImportFailure{Code: "UPSTREAM_REJECTED", Summary: "货源拒绝请求，请检查商品及协议"}
		}
	case errors.Is(err, ErrRateLimited):
		return ImportFailure{Code: "RATE_LIMITED", Summary: "货源限流或网关拦截，正在等待重试", Retry: true}
	case errors.Is(err, context.DeadlineExceeded):
		return ImportFailure{Code: "TIMEOUT", Summary: "货源请求超时", Retry: true}
	case errors.As(err, &ne):
		return ImportFailure{Code: "NETWORK_ERROR", Summary: "暂时无法连接货源", Retry: true}
	case errors.Is(err, ErrProductDeleted):
		return ImportFailure{Code: "PRODUCT_DELETED", Summary: "上游商品已不存在"}
	case errors.Is(err, ErrProductUnavailable), errors.Is(err, ErrNotSupported):
		return ImportFailure{Code: "UNSUPPORTED", Summary: "商品不支持自动采购或规格不受支持"}
	default:
		return ImportFailure{Code: "INVALID_PRODUCT", Summary: "货源未返回完整有效的商品、规格或账号报价"}
	}
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if d, err := time.ParseDuration(value + "s"); err == nil && d > 0 {
		return d
	}
	if at, err := http.ParseTime(value); err == nil && time.Until(at) > 0 {
		return time.Until(at)
	}
	return 0
}
