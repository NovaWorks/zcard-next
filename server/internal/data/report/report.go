// Package report 报表裸 SQL 唯一收口（规划 §3.5 硬原则 3 / 架构测试 §4.10-5）。
//
// 规则：
//   - 只有本包可写裸 SQL，且必须经 platform/db.SQL 原语构造（禁止字符串拼接方言 SQL）；
//   - 查询带 subsite_id 的表时 SQL 必须包含租户条件占位（架构测试 §4.10-12 静态扫描）；
//   - 报表聚合走 daily_stats 日结，不扫大表（§5.18）。
//
// M1b 随 dashboard v1 交付首批查询；当前为收口占位。
package report
