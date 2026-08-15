// Package notify 通知模块（M1 邮件 / M3 全量）：站内信/邮件/短信/Telegram/webhook、
// 事件驱动模板（DB 存储，占位符）、定时定向群发、送达统计。
//
// 表：notifications / notify_templates / notification_logs。
// 降级纪律：SMTP 未配置 → 队列任务标记 skipped 不报错雪崩（友商教训）。
package notify
