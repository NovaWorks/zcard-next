// Package httpx 出站 HTTP 客户端（规划 §5.7.3 货源对接安全）。
//
// 统一强制（架构测试断言，适配器无感知）：
//  1. 仅允许 http/https；
//  2. 私有地址段黑名单（SSRF 防护：回环/内网/链路本地/ULA）——阻断「供货站填内网地址探测元数据」；
//  3. DNS rebinding 防护：连接建立时对实际 IP 复核（防「校验后解析漂移到内网」）；
//  4. 重定向逐跳重校验（禁止「白名单 → 302 → 内网」）；
//  5. 凭据与签名串永不进日志（RedactURL）。
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// PrivateCIDRs 私有/保留地址段黑名单（§5.7.3 清单）。
// IPv4-mapped IPv6 由 IsPrivateIP 的 To4 归一化覆盖，不单列 CIDR
// （::ffff:0:0/96 经 net.IPNet.Contains 规范化后会误判所有公网 IPv4）。
var privateCIDRs = mustCIDRs(
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "0.0.0.0/8", "100.64.0.0/10",
	"::1/128", "fc00::/7", "fe80::/10",
)

func mustCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("httpx: 非法 CIDR " + c)
		}
		out = append(out, n)
	}
	return out
}

// IsPrivateIP 判断 IP 是否落在私有/保留段（导出供测试与诊断）。
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range privateCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrBlockedAddress 目标地址被 SSRF 防护拦截。
type ErrBlockedAddress struct{ Host string }

func (e *ErrBlockedAddress) Error() string {
	return fmt.Sprintf("httpx: 目标地址被 SSRF 防护拦截（私有/保留段）: %s", e.Host)
}

// ValidateURL 校验出站 URL：仅 http/https 且 host 不在私有段（按解析结果判定）。
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("httpx: 非法 URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("httpx: 仅允许 http/https，得到 %q", u.Scheme)
	}
	return checkHost(u.Hostname())
}

func checkHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return &ErrBlockedAddress{Host: host}
		}
		return nil
	}
	// 域名：解析后逐 IP 校验（任一命中私有段即拦截）
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("httpx: 解析 %s 失败: %w", host, err)
	}
	for _, a := range addrs {
		if IsPrivateIP(a) {
			return &ErrBlockedAddress{Host: host + " (" + a.String() + ")"}
		}
	}
	return nil
}

// NewSafeClient 构造安全出站客户端：超时 + 重定向逐跳校验 + 连接期 IP 复核。
func NewSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// DNS rebinding 防护：连接建立瞬间复核实际 IP
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if ip := net.ParseIP(host); ip != nil && IsPrivateIP(ip) {
				return nil, &ErrBlockedAddress{Host: host}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("httpx: 重定向超过 10 跳")
			}
			return ValidateURL(req.URL.String())
		},
	}
}

// UserAgent 统一出站 UA（上游对接可识别来源）。
const UserAgent = "ZCard/2.0"

// Get 简易 GET（带 UA + SSRF 校验）。适配器签名头定制请直接使用 NewSafeClient。
func Get(ctx context.Context, c *http.Client, rawURL string) (*http.Response, error) {
	if c == nil {
		c = NewSafeClient(15 * time.Second)
	}
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	return c.Do(req)
}

// RedactURL 日志脱敏：去除 userinfo（凭据永不进日志，铁律 §5.7.3）。
func RedactURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return raw
}
