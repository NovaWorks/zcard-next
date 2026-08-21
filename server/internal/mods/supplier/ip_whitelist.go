package supplier

// IP 白名单判定（三协议鉴权统一入口）：
//   - 空名单 = 所有 IP 放行（默认，兼容存量账户）；
//   - 非空 = 请求来源 IP 必须命中任一条目（精确 IP 或 CIDR 网段）；
//   - 客户端 IP 取 X-Forwarded-For 首段（反向代理场景），否则 RemoteAddr 主机部分。
//
// 条目格式：IPv4/IPv6 精确地址（1.2.3.4、::1）或 CIDR（10.0.0.0/24、fd00::/8）。

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// maxWhitelistEntries 白名单条目上限（防滥用）。
const maxWhitelistEntries = 20

// ValidateIPWhitelistEntry 校验单条白名单（精确 IP 或 CIDR）。
func ValidateIPWhitelistEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("条目不能为空")
	}
	if strings.Contains(entry, "/") {
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("%q 不是合法的 CIDR 网段", entry)
		}
		return nil
	}
	if net.ParseIP(entry) == nil {
		return fmt.Errorf("%q 不是合法的 IP 地址", entry)
	}
	return nil
}

// requestClientIP 请求来源 IP（XFF 首段优先——经反代时 RemoteAddr 是代理地址）。
func requestClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipAllowed 白名单判定（空名单放行；非空须命中任一条目）。
func ipAllowed(whitelist []string, r *http.Request) bool {
	if len(whitelist) == 0 {
		return true
	}
	clientIP := requestClientIP(r)
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, entry := range whitelist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if entry == clientIP {
			return true
		}
	}
	return false
}
