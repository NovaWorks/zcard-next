// Package procurement 采购模块（ 完整落地）：
// - 状态机：pending → submitted/polling → fulfilled/rejected → refunding → refunded/manual
// （CAS + 迁移表；并发三通道汇聚同一终态只生效一次）
// - 三通道结果获取：指数退避轮询（30s×2/1m×2/2m×2/5m×2/10m，耗尽移交巡检）
// - cron 巡检（30min，24h 卡死转人工）+ 上游回调（ 自家协议）
// - 到手即加密（铁律 11 采购侧）：上游卡密内存态 → CardCipher.Seal（AAD 绑定
// 本地商品/租户）→ 密文落 procurement_items；交付出口复用 fulfillment
// AttachUpstreamDelivery（写 cards(used) + order_deliveries，与本地卡密同一出口）
// - 失败策略：auto_refund（payment.OrderRefunder 创建退款单）| manual（人工终态）
// - 事件：procurement.fulfilled / procurement.failed（outbox 发布）
//
// 表：procurement_orders / procurement_items。
// 订阅：order.paid（order 模块发布，经 outbox；注册在 cmd/zcard newApp 破环点）。
// 调度：轮询走 asynq critical 队列（ProcurementPoll 任务类型），降级模式进程内执行。
package procurement
