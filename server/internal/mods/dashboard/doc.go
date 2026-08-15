// Package dashboard 工作台与报表模块（M1b v1）：指标口径层（data 层独立包，
// 前端只消费不重算）、daily_stats 日结（low 队列聚合，不扫大表）、趋势图、商品排行。
//
// 表：daily_stats / reconciliation_jobs / reconciliation_items（对账并入报表）。
package dashboard
