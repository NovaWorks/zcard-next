package audit

// 风控内核：IP 规范化（IPv6 /64 聚合）+ 黑名单（精确 + CIDR）+ 进程内缓存。
// 友商 redislimiter 模式：黑名单进程内缓存 + 哈希查找；pending 闸门事务内复查。

import (
	"net"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
)

// ipBlacklist IP 黑名单（进程内缓存；admin 变更时失效重建）。
type ipBlacklist struct {
	exact map[string]bool
	cidrs []*net.IPNet
}

// parseBlacklist 解析黑名单条目（精确 IP 或 CIDR；非法条目跳过不阻断）。
func parseBlacklist(entries []string) *ipBlacklist {
	bl := &ipBlacklist{exact: map[string]bool{}, cidrs: nil}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			if _, ipnet, err := net.ParseCIDR(e); err == nil {
				bl.cidrs = append(bl.cidrs, ipnet)
			}
			continue
		}
		// 精确 IP：规范化后存（IPv6 已 /64 聚合——黑名单配置与闸门口径一致）
		bl.exact[port.NormalizeIP(e)] = true
	}
	return bl
}

// contains 命中判定（ip 必须已规范化）。
func (bl *ipBlacklist) contains(ip string) bool {
	if bl == nil {
		return false
	}
	if bl.exact[ip] {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, ipnet := range bl.cidrs {
		if ipnet.Contains(parsed) {
			return true
		}
	}
	return false
}
