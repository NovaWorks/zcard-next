package bootstrap

// 队列/事件/周期任务装配（P0-01 任务书 T2/T3/T4 的 wiring）：
//   - Enqueuer：Redis 配置存在 → AsynqQueue；否则 SyncQueue（Dispatcher 直连 + 死信落库）
//   - Dispatcher：消费分发（processed_events 幂等）；M0 处理器目录为空，M1 随交易闭环注册
//   - Cron：周期任务注册表（M0 空目录；订单超时取消/佣金确认等随 M1+ 在此追加）

import (
	"context"
	"log/slog"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"github.com/google/wire"
	"github.com/hibiken/asynq"
)

var queueProviderSet = wire.NewSet(
	NewEnqueuer,
	NewDispatcher,
	NewOutboxRelay,
	NewCron,
)

// NewEnqueuer 队列决策（ADR-D6 降级矩阵）：conf.Redis.Addr 非空 → asynq；否则同步降级。
func NewEnqueuer(c *conf.Data, dp *data.Dispatcher, dead *data.FailedTaskWriter, logger *slog.Logger) (queue.Enqueuer, func(), error) {
	if c != nil && c.Redis != nil && c.Redis.Addr != "" {
		client := asynq.NewClient(asynq.RedisClientOpt{Addr: c.Redis.Addr})
		return queue.NewAsynqQueue(client), func() { _ = client.Close() }, nil
	}
	logger.Warn("bootstrap.queue.sync_mode", "msg", "未配置 Redis：队列同步降级（失败落 failed_tasks），周期任务走进程内 cron")
	sq := &queue.SyncQueue{
		Log:     logger,
		Handler: dp.HandleTask,
		Dead:    dead,
	}
	return sq, func() {}, nil
}

// NewDispatcher 消费分发器（事件处理器注册点：M1 的 fulfillment/payment/wallet 在此 Register）。
func NewDispatcher(d *data.Data, logger *slog.Logger) *data.Dispatcher {
	dp := data.NewDispatcher(d, logger)
	// M1：dp.Register(data.HandlerReg{Consumer: "fulfillment.order", Type: events.OrderPaid, Fn: ...})
	// M1：周期任务（订单超时取消等）在 NewCron 内注册
	return dp
}

// NewOutboxRelay relay 构造（生命周期由 server.BackgroundServer 托管）。
func NewOutboxRelay(d *data.Data, q queue.Enqueuer, logger *slog.Logger) *data.OutboxRelay {
	return data.NewOutboxRelay(d, q, logger)
}

// NewCron 进程内周期任务（注册表；M0 无任务）。
func NewCron() *queue.Cron {
	c := queue.NewCron()
	// M1 注册（含间隔，规划 §4.8 周期任务清单）：
	//   c.AddEvery("order.expire_scan", time.Minute, orderExpireScan)
	//   c.AddEvery("outbox.dead_alert", 5*time.Minute, ...)
	//   c.AddEvery("commission.confirm", time.Hour, ...)
	_ = c
	_ = context.Background()
	return c
}
