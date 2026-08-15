package server

// BackgroundServer 后台服务托管（kratos.Server 生命周期）：
//   - Outbox relay：所有模式运行（api 进程也需排水事件；SKIP LOCKED + 消费幂等保证多实例安全）
//   - 进程内 cron：仅 all/worker 模式（多实例部署纪律：无 Redis 多实例 = 需单 worker，ADR-D6）
//
// 无 Redis（SyncQueue）时 sync 队列经 Dispatcher 直连分发——单进程即完成「写事件→投递→消费」闭环。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"github.com/go-kratos/kratos/v3/log"
)

// RunMode 进程运行模式（main 注入）。
type RunMode string

// 三种模式（规划 §4.2：api 与 worker 共享全部代码，启动参数选择装配）。
const (
	ModeAll    RunMode = "all"
	ModeAPI    RunMode = "api"
	ModeWorker RunMode = "worker"
)

// BackgroundServer 后台任务 server。
type BackgroundServer struct {
	relay *data.OutboxRelay
	cron  *queue.Cron
	mode  RunMode
}

// NewBackgroundServer 构造。
func NewBackgroundServer(relay *data.OutboxRelay, cron *queue.Cron, mode RunMode) *BackgroundServer {
	return &BackgroundServer{relay: relay, cron: cron, mode: mode}
}

// Start 启动 relay（全模式）与 cron（all/worker 模式）。
func (s *BackgroundServer) Start(ctx context.Context) error {
	go s.relay.Run(ctx)
	if s.mode == ModeAll || s.mode == ModeWorker {
		s.cron.Start(log.Default())
	}
	log.Default().Info("background.started", "mode", string(s.mode), "cron", s.mode != ModeAPI)
	return nil
}

// Stop 停止。
func (s *BackgroundServer) Stop(ctx context.Context) error {
	s.relay.Stop()
	s.cron.Stop()
	return nil
}
