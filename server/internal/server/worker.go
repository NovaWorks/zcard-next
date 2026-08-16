package server

// WorkerServer 队列消费者 server（kratos.Server 包装 asynq.Server）。
// 三队列 weighted 6:3:1（critical/default/low，ADR-D6 修复友商单队列互相阻塞）。
// 无 Redis（降级模式）不装配 asynq——消费经 SyncQueue 在进程内完成（占位即就绪）。
// asynq mux 按任务类型精确匹配（无通配），事件类任务按 events.All() 目录逐一注册，
// 统一进入 Dispatcher 路由；非事件任务（card:import 等）随模块在 mux 上追加注册。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"github.com/go-kratos/kratos/v3/log"
	"github.com/hibiken/asynq"
)

// NewWorkerServer 构造（Redis 未配置时返回降级占位）。
func NewWorkerServer(c *conf.Data, enq queue.Enqueuer, dp *data.Dispatcher, supplySync *supply.SyncService, procureSvc *procurement.ProcureService, supplierAPI *supplier.SupplyAPIService, broadcastSvc *notify.BroadcastService) *WorkerServer {
	w := &WorkerServer{dp: dp}
	if c == nil || c.Redis == nil || c.Redis.Addr == "" || !enq.Enabled() {
		log.Default().Info("worker.sync_mode", "msg", "无 Redis：SyncQueue 进程内消费，asynq 消费者未装配")
		return w
	}
	concurrency := int(c.WorkerConcurrency)
	if concurrency <= 0 {
		concurrency = 10
	}
	opt := asynq.RedisClientOpt{Addr: c.Redis.Addr}
	if c.Redis.Password != "" {
		opt.Password = c.Redis.Password
	}
	if c.Redis.Db > 0 {
		opt.DB = int(c.Redis.Db)
	}
	w.srv = asynq.NewServer(opt, asynq.Config{
		Concurrency: concurrency,
		Queues: map[string]int{
			queue.QueueCritical: 6,
			queue.QueueDefault:  3,
			queue.QueueLow:      1,
		},
	})
	w.mux = asynq.NewServeMux()
	// 事件目录逐一注册到统一入口（asynq mux 无通配）
	for _, typ := range events.All() {
		t := "event:" + typ
		w.mux.HandleFunc(t, func(ctx context.Context, task *asynq.Task) error {
			return dp.HandleTask(ctx, queue.Task{Type: task.Type(), Payload: task.Payload()})
		})
	}
	// 非事件任务（M2）：货源同步（low 队列；payload {"task_id": N}）
	w.mux.HandleFunc(supply.SyncTaskType, func(ctx context.Context, task *asynq.Task) error {
		return supplySync.RunTask(ctx, task.Payload())
	})
	// M2：采购轮询（critical 队列；payload {"procurement_id": N}）
	w.mux.HandleFunc(procurement.PollTaskType, func(ctx context.Context, task *asynq.Task) error {
		return procureSvc.RunPollTask(ctx, task.Payload())
	})
	// M2：下游回调转发（default 队列；payload {"supply_order_id": N}）
	w.mux.HandleFunc(supplier.CallbackTaskType, func(ctx context.Context, task *asynq.Task) error {
		return supplierAPI.RunCallbackTask(ctx, task.Payload())
	})
	// M3：通知群发（default 队列；payload {"broadcast_id": N}）
	w.mux.HandleFunc(notify.BroadcastTaskType, func(ctx context.Context, task *asynq.Task) error {
		return broadcastSvc.RunBroadcastTask(ctx, task.Payload())
	})
	return w
}

// WorkerServer asynq 消费者包装。
type WorkerServer struct {
	srv *asynq.Server
	mux *asynq.ServeMux
	dp  *data.Dispatcher
}

// Start 启动消费者（降级模式空操作）。
func (w *WorkerServer) Start(_ context.Context) error {
	if w.srv == nil {
		return nil
	}
	go func() {
		if err := w.srv.Run(w.mux); err != nil {
			log.Default().Error("worker.asynq_run_failed", "err", err)
		}
	}()
	log.Default().Info("worker.started", "queues", "critical:6 default:3 low:1")
	return nil
}

// Stop 优雅停止。
func (w *WorkerServer) Stop(_ context.Context) error {
	if w.srv != nil {
		w.srv.Shutdown()
	}
	return nil
}
