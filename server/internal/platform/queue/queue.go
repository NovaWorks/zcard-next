// Package queue 任务队列抽象（ADR-D6：asynq 可选依赖）。
//
// 有 Redis：asynq 三队列隔离（critical=交付/采购/补单 6 : default=邮件/通知 3 : low=同步/报表 1），
// 修复友商单队列互相阻塞问题；无 Redis：同步降级执行 + 失败落 failed_tasks 表供手动重放，
// 周期任务由进程内 cron 兜底（多实例部署时要求单 worker 模式）。
//
// M0 定义契约；asynq 适配器与进程内 cron M1 随交易闭环交付。
package queue

import (
	"context"
	"errors"
	"log/slog"
)

// 队列分级（asynq weighted priority 6:3:1）。
const (
	QueueCritical = "critical" // 订单交付、采购提交、支付补单
	QueueDefault  = "default"  // 邮件/短信/通知/回调转发
	QueueLow      = "low"      // 库存价格同步、报表聚合、巡检
)

// Task 异步任务描述。
type Task struct {
	// Type 任务类型（如 "event:order.paid"、"card:import"）
	Type string
	// Payload 任务载荷
	Payload []byte
	// Queue 目标队列（默认 default）
	Queue string
	// DedupeKey 幂等键（可选；asynq TaskID 语义）
	DedupeKey string
}

// Enqueuer 入队端口。Enabled() 为 false 表示降级模式（无 Redis），
// 调用方据此决定同步执行或走降级路径（友商 queue.Enabled() 纪律）。
type Enqueuer interface {
	Enqueue(ctx context.Context, task Task) error
	Enabled() bool
}

// ErrQueueDisabled 队列不可用（降级信号，非错误）。
var ErrQueueDisabled = errors.New("queue: 已禁用（无 Redis，走同步降级）")

// SyncQueue 无 Redis 时的同步降级实现：任务在独立 goroutine 顺序执行（fire-and-forget），
// 失败仅记日志——M1 起失败落 failed_tasks 表供重放。
type SyncQueue struct {
	Log    *slog.Logger
	Handle func(ctx context.Context, task Task) error
}

// Enqueue 同步降级入队。
func (q *SyncQueue) Enqueue(ctx context.Context, task Task) error {
	if q.Handle == nil {
		return nil
	}
	go func() {
		if err := q.Handle(context.WithoutCancel(ctx), task); err != nil {
			if q.Log != nil {
				q.Log.Error("queue.sync.execute_failed", "type", task.Type, "err", err)
			}
		}
	}()
	return nil
}

// Enabled 恒 false：降级模式标识。
func (*SyncQueue) Enabled() bool { return false }
