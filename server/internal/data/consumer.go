package data

// 事件/任务消费分发器（ / 消费侧）：
// asynq worker 与 SyncQueue 共用同一入口；消费幂等经 processed_events(event_id, consumer)。
// 注册空目录（事件消费方 随交易闭环接入）；进程内处理器供单机模式直连分发。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

// HandlerReg 处理器注册项。
type HandlerReg struct {
	Consumer string // 消费者标识（processed_events 幂等键组成；建议 "模块.处理器"）
	Type     string // 订阅的事件类型（如 events.OrderPaid）
	Fn       events.Handler
}

// Dispatcher 消费分发器：任务载荷 = events.Envelope JSON。
type Dispatcher struct {
	data *Data
	log  *slog.Logger

	mu       sync.RWMutex
	handlers map[string][]HandlerReg // 事件类型 → 订阅者
}

// NewDispatcher 构造。
func NewDispatcher(d *Data, log *slog.Logger) *Dispatcher {
	return &Dispatcher{data: d, log: log, handlers: map[string][]HandlerReg{}}
}

// Register 注册订阅（幂等：同 Consumer+Type 重复注册覆盖）。
func (dp *Dispatcher) Register(regs ...HandlerReg) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	for _, r := range regs {
		if r.Consumer == "" || r.Type == "" || r.Fn == nil {
			continue
		}
		list := dp.handlers[r.Type]
		replaced := false
		for i := range list {
			if list[i].Consumer == r.Consumer {
				list[i] = r
				replaced = true
			}
		}
		if !replaced {
			dp.handlers[r.Type] = append(list, r)
		}
	}
}

// HandleTask 队列任务入口（task type 形如 "event:order.paid"）。
func (dp *Dispatcher) HandleTask(ctx context.Context, task queue.Task) error {
	typ := strings.TrimPrefix(task.Type, "event:")
	var env events.Envelope
	if err := json.Unmarshal(task.Payload, &env); err != nil {
		return fmt.Errorf("consumer: 解析事件载荷失败（type=%s）: %w", task.Type, err)
	}
	if env.Type == "" {
		env.Type = typ
	}
	return dp.Dispatch(ctx, env)
}

// Dispatch 分发到全部订阅者（逐个幂等；单订阅者失败不阻断其余，返回聚合错误）。
func (dp *Dispatcher) Dispatch(ctx context.Context, env events.Envelope) error {
	dp.mu.RLock()
	subs := append([]HandlerReg(nil), dp.handlers[env.Type]...)
	dp.mu.RUnlock()
	var errs []error
	for _, sub := range subs {
		if err := dp.runOnce(ctx, env, sub); err != nil {
			errs = append(errs, fmt.Errorf("consumer %s: %w", sub.Consumer, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("consumer: %v", errs)
	}
	return nil
}

// runOnce 单订阅者执行：processed_events 唯一索引幂等（已处理直接返回 nil）。
func (dp *Dispatcher) runOnce(ctx context.Context, env events.Envelope, sub HandlerReg) error {
	_, err := Client(ctx, dp.data).ProcessedEvent.Create().
		SetEventID(env.EventID).
		SetConsumer(sub.Consumer).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return nil // 已消费：幂等 ACK
	}
	if err != nil {
		return err
	}
	if err := sub.Fn(ctx, env); err != nil {
		return err // 处理失败：processed_events 已写入—— 引入补偿删除或改「先执行后记录」策略前，以日志告警
	}
	if dp.log != nil {
		dp.log.Debug("consumer.dispatched", "type", env.Type, "consumer", sub.Consumer, "event_id", env.EventID)
	}
	return nil
}
