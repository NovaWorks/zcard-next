package adapter

// 出站传输封装：统一 httpx 安全客户端 + 重试退避 + 日志脱敏。
// 架构测试断言本包不得直接构造 http.Client（一律经 platform/httpx）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
)

// httpError 上游非 2xx 的结构化错误。
type httpError struct {
	Status  int
	Code    string // 上游 error_code（如 product_unavailable）
	Message string
}

func (e *httpError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("adapter: upstream %d (%s): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("adapter: upstream status %d", e.Status)
}

// upstreamErrorCode 从错误链提取 error_code（哨兵归一化判据）。
func upstreamErrorCode(err error) string {
	var he *httpError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

// transport 出站传输（一实例一适配器）。
type transport struct {
	baseURL string
	client  *http.Client
	// retryIntervals 网络错误/5xx 的重试退避（秒），由 connection.retry_intervals 驱动
	retryIntervals []int
	log            *slog.Logger
}

// validateURL 出站 URL 校验（SSRF）。包内变量便于测试注入（httptest 用回环地址）。
var validateURL = httpx.ValidateURL

// newTransport 构造传输。baseURL 立即做 SSRF 校验（连接创建路径也校验，双保险）。
func newTransport(baseURL string, retryIntervals []int, log *slog.Logger) (*transport, error) {
	if err := validateURL(baseURL); err != nil {
		return nil, err
	}
	return newTransportWithClient(baseURL, retryIntervals, log, httpx.NewSafeClient(30*time.Second)), nil
}

// newTransportWithClient 注入自定义 client（测试用：httptest 回环地址在 httpx
// 连接期复核会被拦，须注入普通 client；生产一律走 newTransport）。
func newTransportWithClient(baseURL string, retryIntervals []int, log *slog.Logger, client *http.Client) *transport {
	intervals := retryIntervals
	if len(intervals) == 0 {
		intervals = []int{30, 60, 300}
	}
	return &transport{
		baseURL:        strings.TrimRight(baseURL, "/"),
		client:         client,
		retryIntervals: intervals,
		log:            log,
	}
}

// do 发送请求并返回响应体。headers 中的签名头由各协议适配器构造；
// body 为实际发出的字节（签名哈希 === 实际字节 不变式的发送端）。
func (t *transport) do(ctx context.Context, method, path string, query url.Values, headers map[string]string, body []byte) ([]byte, error) {
	full := t.baseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	var lastErr error
	attempts := 1 + len(t.retryIntervals)
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := time.Duration(t.retryIntervals[i-1]) * time.Second
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		resp, err := t.tryOnce(ctx, method, full, headers, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// 业务错误（上游明确拒绝）不重试；仅网络错误/5xx 重试
		var he *httpError
		if errors.As(err, &he) && he.Status < 500 && he.Status > 0 {
			return nil, err
		}
		t.log.Warn("adapter.http.retry",
			"method", method, "url", httpx.RedactURL(full), "attempt", i+1, "err", err)
	}
	return nil, lastErr
}

func (t *transport) tryOnce(ctx context.Context, method, full string, headers map[string]string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return nil, fmt.Errorf("adapter: 构造请求失败: %w", err)
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adapter: 请求 %s 失败: %w", httpx.RedactURL(full), err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("adapter: 读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, msg := parseErrorPayload(respBody)
		return nil, &httpError{Status: resp.StatusCode, Code: code, Message: msg}
	}
	return respBody, nil
}

// parseErrorPayload 解析上游统一错误体 {error_code, error_message}
// （zcard/dujiao 口径；acg-faka 为 {code, msg}，见 acgfaka.go 单独处理）。
func parseErrorPayload(body []byte) (code, msg string) {
	if len(body) == 0 {
		return "", ""
	}
	var payload struct {
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Msg          string `json:"msg"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	if payload.ErrorCode != "" {
		return payload.ErrorCode, payload.ErrorMessage
	}
	if payload.Msg != "" {
		return "", payload.Msg
	}
	return "", ""
}
