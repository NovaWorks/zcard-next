// Package notify 通知中心（P2-05 T1-T3 落地）：
//   - 通道：Email（SMTP 运行时配置——变更不重启；未配置/禁用 → skipped 降级不报错）
//     + Inbox（站内信 + 铃铛 API）；SMS/Telegram M3 交付（Channel 接口位已留）
//   - 分发器：outbox 事件 → 路由表（事件 → 通道集合）→ 模板渲染（占位符子集
//     {{.key}} 白名单变量 + 值 HTML escape——双保险防注入）→ 逐通道独立投递 →
//     每条落 notification_logs（sent/failed/skipped）
//   - 模板：事件 × 通道 × 语言（zh_CN 回落）；后台 CRUD + 样例预览 + 白名单校验
//   - 订阅事件：order.paid/delivered/completed/canceled/refunded、payment.failed、
//     recharge.succeeded、user.registered（main.go 注册）
//   - 显式发送：Dispatcher.Send（notifyport.Sender——业务模块管理员告警直调）
//
// 表：notifications / notify_templates / notification_logs。
// 幂等：processed_events(event_id, consumer) 由 Dispatcher 统一兜底。
package notify
