// Package queue 任务队列抽象（ADR-D6：asynq 可选依赖）。
//
// 有 Redis：asynq 三队列隔离（critical=交付/采购/补班 6 : default=邮件/通知 3 : low=同步/报表 1），
// 修复友商单队列互相阻塞问题；无 Redis：同步降级执行 + 失败落 failed_tasks 表供手动重放，
// 周期任务由进程内 cron 兜底（多实例部署时要求单 worker 模式）。
package queue

import (
	"context"
	"log/slog"
	"time"
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
	// Queue 目标队列（空 = default）
	Queue string
	// DedupeKey 幂等键（asynq TaskID 语义：同 key 重复入队直接成功）
	DedupeKey string
}

// Enqueuer 入队端口。Enabled() 为 false 表示降级模式（无 Redis），
// 调用方据此决定同步执行或走降级路径（友商 queue.Enabled() 纪律）。
type Enqueuer interface {
	Enqueue(ctx context.Context, task Task) error
	Enabled() bool
}

// FailedTaskWriter 死信落库端口（无 Redis 降级模式使用）。
// 实现位于 internal/data（failed_tasks 表）；platform 不 import ent。
type FailedTaskWriter interface {
	SaveFailedTask(ctx context.Context, taskType string, payload []byte, errMsg string) error
}

// SyncQueue 无 Redis 时的同步降级实现：任务在独立 goroutine 顺序执行（fire-and-forget），
// 失败落 failed_tasks（经 FailedTaskWriter；writer 缺失时仅记日志）。
type SyncQueue struct {
	Log     *slog.Logger
	Handler func(ctx context.Context, task Task) error
	Dead    FailedTaskWriter
}

// Enqueue 同步降级入队。
func (q *SyncQueue) Enqueue(ctx context.Context, task Task) error {
	if q.Handler == nil {
		return nil
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := q.Handler(runCtx, task); err != nil {
			q.recordDead(runCtx, task, err)
		}
	}()
	return nil
}

// Enabled 恒 false：降级模式标识。
func (*SyncQueue) Enabled() bool { return false }

func (q *SyncQueue) recordDead(ctx context.Context, task Task, cause error) {
	if q.Dead != nil {
		if err := q.Dead.SaveFailedTask(ctx, task.Type, task.Payload, cause.Error()); err == nil {
			return
		}
		// 落库失败继续走日志（不死循环）
	}
	if q.Log != nil {
		q.Log.Error("queue.sync.execute_failed", "type", task.Type, "err", cause)
	}
}
