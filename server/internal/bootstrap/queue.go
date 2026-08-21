package bootstrap

// 队列/事件/周期任务装配（P0-01 任务书 T2/T3/T4 的 wiring）：
//   - Enqueuer：Redis 配置存在 → AsynqQueue；否则 SyncQueue（Dispatcher 直连 + 死信落库）
//   - Dispatcher：消费分发（processed_events 幂等）；M0 处理器目录为空，M1 随交易闭环注册
//   - Cron：周期任务注册表（M0 空目录；订单超时取消/佣金确认等随 M1+ 在此追加）

import (
	"context"
	"log/slog"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/affiliate"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
	"github.com/NovaWorks/zcard-next/server/internal/mods/ticket"
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
		opt := asynq.RedisClientOpt{Addr: c.Redis.Addr}
		if c.Redis.Password != "" {
			opt.Password = c.Redis.Password
		}
		if c.Redis.Db > 0 {
			opt.DB = int(c.Redis.Db)
		}
		client := asynq.NewClient(opt)
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

// NewDispatcher 消费分发器（事件处理器注册点）。
// 订阅注册在 cmd/zcard newApp 装配（wire 破环：Dispatcher → ProcureService → Enqueuer → Dispatcher）。
func NewDispatcher(d *data.Data, logger *slog.Logger) *data.Dispatcher {
	return data.NewDispatcher(d, logger)
}

// NewOutboxRelay relay 构造（生命周期由 server.BackgroundServer 托管）。
func NewOutboxRelay(d *data.Data, q queue.Enqueuer, logger *slog.Logger) *data.OutboxRelay {
	return data.NewOutboxRelay(d, q, logger)
}

// NewCron 进程内周期任务（注册表）。
func NewCron(supplySync *supply.SyncService, supplyScheduler *supply.Scheduler, procure *procurement.ProcureService, supplierRepo *supplier.SupplierRepoImpl, auditRepo *audit.AuditRepo, visitCounter *audit.VisitCounter, trackRepo *audit.TrackRepo, broadcastSvc *notify.BroadcastService, ticketAdmin *ticket.AdminTicketService, affiliateSvc *affiliate.AffiliateService) *queue.Cron {
	c := queue.NewCron()
	// M2：货源连接周期探活（健康度累计 → M4 供应商评分基础数据，P2-01 T5）
	c.AddEvery("supply.health_ping", 5*time.Minute, func(ctx context.Context) {
		supplySync.PingAllActive(ctx)
	})
	// P2-10 S3：定时同步调度（每分钟扫描 settings.schedule 到期任务）+ 看门狗（僵死任务回收）
	c.AddEvery("supply.schedule_scan", time.Minute, supplyScheduler.Scan)
	c.AddEvery("supply.sync_reap_stale", 10*time.Minute, supplyScheduler.ReapStaleTasks)
	// M2：采购巡检兜底（每 30 分钟拉 polling/submitted 单查上游；24h 卡死转人工）
	c.AddEvery("procurement.patrol", 30*time.Minute, procure.Patrol)
	// M2：供货 nonce 过期清理（每小时）
	c.AddEvery("supplier.nonce_cleanup", time.Hour, func(ctx context.Context) {
		_ = supplierRepo.CleanupExpiredNonces(ctx)
	})
	// M2：风控锁过期清理（每 5 分钟）+ 访问统计批量落库（每分钟）
	c.AddEvery("audit.lock_cleanup", 5*time.Minute, func(ctx context.Context) {
		_ = auditRepo.CleanupExpiredLocks(ctx)
	})
	c.AddEvery("audit.visit_flush", time.Minute, func(ctx context.Context) {
		visitCounter.Flush()
	})
	// T5：访问明细/在线心跳清理（每小时——明细保留 90 天，心跳保留 24 小时）
	c.AddEvery("audit.visit_cleanup", time.Hour, func(ctx context.Context) {
		_ = trackRepo.CleanupVisitData(ctx)
	})
	// M3：定时群发扫描（到期 pending → 入队）
	c.AddEvery("notify.broadcast_scan", time.Minute, broadcastSvc.ScanDue)
	// M3：工单 resolved 超 7 天自动关闭
	c.AddEvery("ticket.autoclose", time.Hour, ticketAdmin.AutoCloseResolved)
	// M3：佣金到期确认（冻结期过 → wallet 入账；负债行重试抵扣）
	c.AddEvery("affiliate.confirm", time.Hour, affiliateSvc.ConfirmDue)
	return c
}
