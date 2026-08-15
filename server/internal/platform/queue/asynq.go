package queue

// asynq 适配器（有 Redis 时启用；三队列 weighted 6:3:1 隔离，ADR-D6）。

import (
	"context"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
)

// AsynqQueue asynq 实现（Enqueuer）。Enabled 恒 true——构造即代表 Redis 已配置。
type AsynqQueue struct {
	client *asynq.Client
}

// NewAsynqQueue 构造（client 生命周期由调用方管理）。
func NewAsynqQueue(client *asynq.Client) *AsynqQueue { return &AsynqQueue{client: client} }

// TaskOf 构造 asynq 任务（导出供 worker 侧注册同一口径的处理器名）。
func TaskOf(t Task) *asynq.Task {
	typ := t.Type
	if typ == "" {
		typ = "unknown"
	}
	return asynq.NewTask(typ, t.Payload)
}

// Enqueue 入队：队列名归一（空→default）+ DedupeKey → asynq TaskID（唯一约束即幂等）。
func (q *AsynqQueue) Enqueue(ctx context.Context, t Task) error {
	name := t.Queue
	if name == "" {
		name = QueueDefault
	}
	opts := []asynq.Option{asynq.Queue(name)}
	if t.DedupeKey != "" {
		opts = append(opts, asynq.TaskID(t.DedupeKey))
	}
	info, err := q.client.EnqueueContext(ctx, TaskOf(t), opts...)
	if err != nil {
		// 同 TaskID 已存在视为成功（幂等重入）
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return fmt.Errorf("queue: enqueue %q 到 %q 失败: %w", t.Type, name, err)
	}
	_ = info
	return nil
}

// Enabled 恒 true。
func (*AsynqQueue) Enabled() bool { return true }

// NewRedisClient 按 conf 构造 asynq 底层 Redis 连接。
func NewRedisClient(opt asynq.RedisConnOpt) *asynq.Client {
	return asynq.NewClient(opt)
}
