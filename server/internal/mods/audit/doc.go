// Package audit 审计与风控模块（ 完整落地）：
// - 操作审计（）：变更类 admin 操作（POST/PUT/DELETE）自动落 audit_logs；
// before/after 快照经注册表采集（各模块声明聚合键）；异步落库失败不阻断
// - 安全审计（）：port.Auditor 埋点（登录/解密/导出/权限变更/黑名单变更/
// 取货）——写失败不阻断业务（1.x 纪律）；取货审计含谁/何时/IP/订单（零明文）
// - 风控闸门（）：port.RiskGate——IP 黑名单（精确+CIDR，进程内缓存）→
// pending 订单闸门（同 IP 超限拒绝，DB 事务内复查防并发穿透）→ 频率限流
// （每 IP 每分钟滑动窗口）；取货连续失败锁定（risk_lock_keys TTL，锁定期内
// 正确密码也拒绝）；IPv6 /64 聚合（orders.risk_ip 写入口统一）
// - 访问统计（）：VisitCounter 进程内聚合（阈值 100 批量落库 + cron 每分钟
// 兜底 Flush）——不逐请求写库
//
// 表：audit_logs / security_audit_logs / visit_logs / risk_lock_keys。
// 阈值常量：MaxPendingPerIP=3 / FetchFailLockN=5 / FetchLockTTL=30min /
// OrderPerMinPerIP=10（settings.security 读取侧 接线）。
package audit
