// Package procurement 采购模块（M2）：上游采购单状态机、提交/轮询/巡检三通道
// 结果获取、失败策略（自动退款/转人工）、上游退款传导。
//
// 表：procurement_orders / procurement_items（显式建模 items，不隐含单商品约定）。
// 依赖：supply（协议适配器）；订阅 payment.succeeded / order.paid（含 upstream 项）。
// 三通道：上游主动回调 + 指数退避轮询（30s~10min 约 30 分钟）+ 每 30 分钟巡检兜底（24h 卡死告警）。
package procurement
