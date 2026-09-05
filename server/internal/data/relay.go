package data

// Outbox relay（规划 ； ）：500ms 扫描 publishing 事件 →
// 入队（asynq 或进程内同步分发）→ 标记 published。
//
// 多实例安全：MySQL/PG 认领用 FOR UPDATE SKIP LOCKED；SQLite 单写者事务天然串行
// （BATCH 内一次事务认领+投递后标记，崩溃由至少一次投递 + 消费幂等兜底）。

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"entgo.io/ent/dialect/sql"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/outboxevent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

const (
	relayInterval = 500 * time.Millisecond
	relayBatch    = 100
	relayMaxTry   = 3 // 连续投递失败次数上限，超过置 failed + 告警日志
)

// OutboxRelay outbox 投递器（kratos server 生命周期托管，见 internal/server/background.go）。
type OutboxRelay struct {
	data  *Data
	queue queue.Enqueuer
	log   *slog.Logger

	mu      sync.Mutex
	attempt map[uint64]int // 内存重试计数（进程重启归零，由消费幂等兜底）
	stop    chan struct{}
	stopOne sync.Once
}

// NewOutboxRelay 构造。
func NewOutboxRelay(d *Data, q queue.Enqueuer, log *slog.Logger) *OutboxRelay {
	return &OutboxRelay{
		data: d, queue: q, log: log,
		attempt: map[uint64]int{},
		stop:    make(chan struct{}),
	}
}

// Run 阻塞循环（调用方以 goroutine 托管）；ctx 取消或 Stop 后返回。
func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(relayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-ticker.C:
			if err := r.tick(ctx); err != nil && r.log != nil {
				r.log.Error("outbox.relay.tick_failed", "err", err)
			}
		}
	}
}

// Stop 幂等停止。
func (r *OutboxRelay) Stop() { r.stopOne.Do(func() { close(r.stop) }) }

// tick 单轮：认领一批 publishing → 逐条投递 → 投递成功即标记 published。
func (r *OutboxRelay) tick(ctx context.Context) error {
	rows, err := r.claim(ctx)
	if err != nil || len(rows) == 0 {
		return err
	}
	for _, row := range rows {
		if err := r.deliver(ctx, row); err != nil {
			return nil // 本条失败不阻断后续 tick；计数与置 failed 在 deliver 内处理
		}
	}
	return nil
}

// claim 认领一批：MySQL/PG 行锁 SKIP LOCKED（多实例不重复认领）；SQLite 普通查询
// （单写者串行；崩溃场景由至少一次语义 + 消费幂等兜底）。
func (r *OutboxRelay) claim(ctx context.Context) ([]*ent.OutboxEvent, error) {
	q := Client(ctx, r.data).OutboxEvent.Query().
		Where(outboxevent.StatusEQ(outboxevent.StatusPublishing)).
		Order(ent.Asc(outboxevent.FieldID)).
		Limit(relayBatch)
	if c := r.data.Dialect.Capabilities(); c.SupportsSkipLocked {
		q = q.ForUpdate(entsql.WithLockAction(sql.SkipLocked))
	}
	return q.All(ctx)
}

// deliver 投递单条：入队成功 → published；失败 → 计数，连续 relayMaxTry 次置 failed。
func (r *OutboxRelay) deliver(ctx context.Context, row *ent.OutboxEvent) error {
	env := events.Envelope{
		EventID:     row.ID,
		Type:        row.Type,
		AggregateID: row.AggregateID,
		Payload:     row.Payload, // 事件载荷必须随信封投递（消费方按载荷解析业务字段）
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return r.markFailed(ctx, row, err)
	}
	task := queue.Task{
		Type:    "event:" + row.Type,
		Payload: payload,
		Queue:   queue.QueueCritical, // 事件扇出（交付/采购/补单）默认 critical；细分由订阅方任务类型承接
	}
	if row.Module == "notify" {
		task.Queue = queue.QueueDefault // 通知类事件降级 default
	}
	if err := r.queue.Enqueue(ctx, task); err != nil {
		r.mu.Lock()
		r.attempt[row.ID]++
		n := r.attempt[row.ID]
		r.mu.Unlock()
		if n >= relayMaxTry {
			return r.markFailed(ctx, row, err)
		}
		if r.log != nil {
			r.log.Warn("outbox.relay.enqueue_retry", "event_id", row.ID, "type", row.Type, "attempt", n, "err", err)
		}
		return nil
	}
	r.mu.Lock()
	delete(r.attempt, row.ID)
	r.mu.Unlock()
	_, err = Client(ctx, r.data).OutboxEvent.UpdateOne(row).
		SetStatus(outboxevent.StatusPublished).
		SetPublishedAt(time.Now().UTC()).
		Save(ctx)
	return err
}

func (r *OutboxRelay) markFailed(ctx context.Context, row *ent.OutboxEvent, cause error) error {
	if r.log != nil {
		r.log.Error("outbox.relay.event_failed", "event_id", row.ID, "type", row.Type, "err", cause)
	}
	_, err := Client(ctx, r.data).OutboxEvent.UpdateOne(row).
		SetStatus(outboxevent.StatusFailed).
		Save(ctx)
	return err
}

var _ = db.SQLite // 保持 db 包引用（能力判定见 claim）
