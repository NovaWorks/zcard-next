// Package supplier 对外供货模块（M2）：供货商账户、供货余额账本、对外供货 API
// （本站作上游）、下游对接申请、下游回调转发重试。
//
// 表：supplier_accounts / supplier_ledger_entries / supply_orders / supply_nonces /
// downstream_callbacks / supplier_product_prices。
// 账本幂等键：supply_order:<downID>；本 API 与 M4 中心聚合平台是同一套协议两种拓扑。
package supplier
