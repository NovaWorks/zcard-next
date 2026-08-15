// Package audit 审计模块（M1）：管理员操作审计（变更类管理操作经拦截中间件落
// audit_logs：操作者/权限点/前后快照/IP）、security_audit_logs（登录失败/异地/敏感操作）、
// visit_logs（轻量聚合）。
//
// 敏感操作结构化日志（解密/取货/导出/权限变更/密钥轮换）：事件名 + 关键 ID，明文永不入日志。
package audit
