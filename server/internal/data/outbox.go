package data

// 事务性 Outbox 写入实现（规划 §4.8 通道 C；P0-01 任务书 T1）。
//
// 关键约束：Write 必须在业务事务内调用——经 data.Tx 携带的 *ent.Tx 落库，
// 业务回滚事件不残留。dedupe_key 唯一索引兜底：重复发布幂等返回 nil。

import (
	"context"
	"encoding/json"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

// OutboxWriter events.Writer 实现（wire 绑定）。
type OutboxWriter struct {
	data *Data
}

// NewOutboxWriter 构造。
func NewOutboxWriter(d *Data) *OutboxWriter { return &OutboxWriter{data: d} }

// Write 写入 outbox 事件（同事务约束见文件头注释）。
// dedupe_key 冲突（同事件重复发布，如订单重复回调）幂等返回 nil。
// 租户信息由各模块事件载荷自带（outbox_events 表无 subsite_id 列，数据库架构 §4.8）。
func (w *OutboxWriter) Write(ctx context.Context, module, typ, aggregateID, dedupeKey string, payload json.RawMessage) error {
	_, err := Client(ctx, w.data).OutboxEvent.Create().
		SetModule(module).
		SetType(typ).
		SetAggregateID(aggregateID).
		SetDedupeKey(dedupeKey).
		SetPayload(payload).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return nil
	}
	return err
}

var _ events.Writer = (*OutboxWriter)(nil)

// FailedTaskWriter 实现（无 Redis 降级死信落库，platform/queue.FailedTaskWriter）。
type FailedTaskWriter struct{ data *Data }

// NewFailedTaskWriter 构造。
func NewFailedTaskWriter(d *Data) *FailedTaskWriter { return &FailedTaskWriter{data: d} }

// SaveFailedTask 落 failed_tasks 表（供 M2 `zcard admin retry-task` 重放）。
func (w *FailedTaskWriter) SaveFailedTask(ctx context.Context, taskType string, payload []byte, errMsg string) error {
	_, err := Client(ctx, w.data).FailedTask.Create().
		SetTaskType(taskType).
		SetPayload(payload).
		SetError(errMsg).
		Save(ctx)
	return err
}

var _ queue.FailedTaskWriter = (*FailedTaskWriter)(nil)
