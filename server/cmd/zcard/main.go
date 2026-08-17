// zcard 入口（规划 §4.3 / §10.2–10.3）。
//
// 用法：
//
//	zcard serve [-conf configs] [-mode all|api|worker]   # 启动服务（默认 all）
//	zcard migrate [-conf configs]                        # 应用待执行迁移后退出
//	zcard admin create|list|reset-password ...           # 运维子命令（同二进制，容器免工具）
//	zcard install                                        # 安装向导（M1）
//	zcard version                                        # 版本信息
//
// 运行模式：api 与 worker 共享全部代码，靠 -mode 选择装配的 server（worker 模式
// 的 asynq 消费者与周期任务 M1 随交易闭环交付；M0 仅 api/all）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/admincmd"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/affiliate"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/reseller"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/updater"
	"github.com/NovaWorks/zcard-next/server/internal/server"

	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/transport"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"go.uber.org/automaxprocs/maxprocs"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	// Name 服务名。
	Name = "zcard"
	// Version 构建注入版本。
	Version string

	id, _ = os.Hostname()

	// appMode 运行模式（runServe 解析 -mode 后赋值，wire 经 provideRunMode 消费）
	appMode = string(server.ModeAll)
)

// provideRunMode wire provider（server 装配按模式分流）。
func provideRunMode() server.RunMode { return server.RunMode(appMode) }

func main() {
	// 容器 CPU 限额感知（GOMAXPROCS 修正）
	_, _ = maxprocs.Set(maxprocs.Logger(func(f string, a ...any) { slog.Debug(fmt.Sprintf(f, a...)) }))

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "migrate":
		err = runMigrate(args)
	case "admin":
		err = admincmd.Run(args)
	case "reencrypt-cards":
		err = admincmd.RunReencrypt(args)
	case "install":
		err = runInstall(args)
	case "self-update":
		err = runSelfUpdate(args)
	case "version":
		fmt.Printf("%s %s (%s)\n", Name, Version, id)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %q\n\n", cmd)
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "zcard: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`zcard —— ZCard 2.0 单二进制服务

用法:
  zcard serve  [-conf <dir>] [-mode all|api|worker]   启动服务（默认 all）
  zcard migrate [-conf <dir>]                          应用待执行迁移后退出
  zcard admin  create|list|reset-password              运维子命令
  zcard reencrypt-cards --new-key <hex>                卡密密钥轮换
  zcard self-update [--check|--rollback]               在线更新（ed25519 验签；--check 只查）
  zcard install                                        安装向导（M1 交付）
  zcard version                                        版本信息

通用 flag:
  -conf <dir>   配置目录（默认 configs；含 config.yaml）
`)
}

// loadBootstrap 装载配置（file source；运行时业务开关在 settings 表，铁律 7）。
func loadBootstrap(confDir string) (*conf.Bootstrap, error) {
	if confDir == "" {
		confDir = "configs"
	}
	c := config.New(config.WithSource(file.NewSource(confDir)))
	defer c.Close()
	if err := c.Load(); err != nil {
		return nil, fmt.Errorf("读取配置失败（目录 %s）: %w", confDir, err)
	}
	bc := &conf.Bootstrap{}
	if err := c.Scan(bc); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if bc.Security == nil {
		bc.Security = &conf.Security{}
	}
	return bc, nil
}

// newLogger slog 构造（text/json + 级别；结构化：事件名 + 关键 ID，§10.5）。
func newLogger(bc *conf.Bootstrap) *slog.Logger {
	level := slog.LevelInfo
	format := "text"
	if bc.Log != nil {
		switch strings.ToLower(bc.Log.Level) {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
		format = bc.Log.Format
	}
	hOpts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewTextHandler(os.Stdout, hOpts)
	if strings.ToLower(format) == "json" {
		h = slog.NewJSONHandler(os.Stdout, hOpts)
	}
	return slog.New(h).With(slog.String("service.id", id), slog.String("service.name", Name), slog.String("service.version", Version))
}

// applyMigrationsIfEnabled 启动迁移（§10.4：加锁串行，失败拒绝启动）。
func applyMigrationsIfEnabled(ctx context.Context, bc *conf.Bootstrap) error {
	if bc.Server != nil && !bc.Server.MigrateOnStart {
		return nil
	}
	d, cleanup, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := data.ApplyMigrations(ctx, d.DB, d.Dialect, bc.Data.Database.Source); err != nil {
		return fmt.Errorf("启动迁移失败（拒绝启动，规划 §10.4）: %w", err)
	}
	return nil
}

// runServe 启动服务。
func runServe(args []string) (err error) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	mode := fs.String("mode", "all", "运行模式 all|api|worker")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bc, err := loadBootstrap(*confDir)
	if err != nil {
		return err
	}
	logger := newLogger(bc)
	log.SetDefault(logger)

	// 更新 pending 态：serve 任何启动失败（配置/迁移/装配）自动回滚二进制，
	// systemd 重启即旧版本（§10.4 自动回滚；DB 只前滚，靠备份恢复）。
	binPath := currentBinaryPath()
	pending := false
	if binPath != "" {
		if st, lerr := updater.LoadState(binPath); lerr == nil && st.Status == updater.StatePending {
			pending = true
			logger.Warn("update.pending", "from", st.FromVer, "to", st.ToVer)
			defer func() {
				if err != nil && updater.RollbackOnBootFailure(binPath) {
					logger.Error("update.rollback.applied", "reason", "boot_failure", "from", st.FromVer)
				}
			}()
		}
	}

	if err := applyMigrationsIfEnabled(context.Background(), bc); err != nil {
		return err
	}
	switch *mode {
	case string(server.ModeAll), string(server.ModeAPI), string(server.ModeWorker):
		appMode = *mode
	default:
		return fmt.Errorf("未知运行模式 %q（all|api|worker）", *mode)
	}
	logger.Info("zcard.starting", "mode", appMode, "version", Version)

	supplier.ServerVersion = orDev(Version)
	app, cleanup, err := wireApp(bc.Server, bc.Data, bc.Security, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	// 更新健康门（§10.4）：HTTP 就绪 + DB 连通自检通过 → pending 转 ok；
	// 超时 → 回滚并优雅退出，systemd 重启旧版本。
	if pending {
		go func() {
			herr := updater.HealthGate(context.Background(), healthURL(bc), binPath, 3*time.Minute)
			if herr == nil {
				logger.Info("update.healthgate.ok", "version", Version)
				return
			}
			logger.Error("update.healthgate.timeout", "error", herr)
			if updater.RollbackOnBootFailure(binPath) {
				logger.Error("update.rollback.applied", "reason", "healthgate_timeout")
			}
			_ = app.Stop()
		}()
	}
	return app.Run()
}

// currentBinaryPath 当前二进制绝对路径（失败返回空串，更新态检查跳过）。
func currentBinaryPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return p
}

// healthURL 健康门探测地址（HTTP 监听口 → 127.0.0.1 本机自检）。
func healthURL(bc *conf.Bootstrap) string {
	addr := ""
	if bc.Server != nil && bc.Server.Http != nil {
		addr = bc.Server.Http.Addr
	}
	if addr == "" {
		addr = "0.0.0.0:8000"
	}
	parts := strings.Split(addr, ":")
	return "http://127.0.0.1:" + parts[len(parts)-1] + "/health"
}

// runMigrate 仅执行迁移。
func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bc, err := loadBootstrap(*confDir)
	if err != nil {
		return err
	}
	if err := applyMigrationsIfEnabled(context.Background(), bc); err != nil {
		return err
	}
	fmt.Println("迁移完成（无待执行项或已全部应用）")
	return nil
}

// runInstall 安装向导（P0-04：CLI 交互式；Web /install M1b 前端补齐）。
func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bc, err := loadBootstrap(*confDir)
	if err != nil {
		return err
	}
	d, cleanup, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer cleanup()
	return settings.RunInstallCLI(context.Background(), d)
}

// newApp kratos.App 装配：按模式选择 server 组合（规划 §4.2 单进程多角色）。
//
//	all    = HTTP + gRPC + worker + 后台（默认，单机形态）
//	api    = HTTP + gRPC + 后台relay（多实例 api，cron 不注册）
//	worker = worker + 后台（消费与周期任务，多实例 asynq 竞争消费）
func newApp(logger *slog.Logger, hs *khttp.Server, gs *kgrpc.Server, ws *server.WorkerServer, bs *server.BackgroundServer, dp *data.Dispatcher, procureSvc *procurement.ProcureService, notifyDisp *notify.Dispatcher, affiliateSvc *affiliate.AffiliateService, resellerSettleSvc *reseller.SettleService, fulfillRepo *fulfillment.DeliveryRepoImpl, pointsSvc *memberlevel.PointsService, orderUC *order.OrderUsecase, payRepo *payment.PaymentRepoImpl) *kratos.App {
	// P1-03 破环点：order 超时取消慢通道顺延探测 ← payment 实现
	// （wire 环 OrderUsecase ↔ PaymentRepoImpl，装配期手工注入——同 dp.Register 模式）
	orderUC.SetSlowPaymentChecker(payRepo)
	// 事件订阅注册（P2-02）：order.paid → 采购（wire 破环点，见 bootstrap/queue.go 注释）
	dp.Register(data.HandlerReg{
		Consumer: "procurement.order_paid",
		Type:     events.OrderPaid,
		Fn:       procureSvc.OnOrderPaid,
	})
	// 事件订阅注册（P3-03）：order.paid → 三级佣金入账；order.refunded → 逆向扣回
	dp.Register(data.HandlerReg{Consumer: "affiliate.settle", Type: events.OrderPaid, Fn: affiliateSvc.OnOrderPaid})
	dp.Register(data.HandlerReg{Consumer: "affiliate.reversal", Type: events.OrderRefunded, Fn: affiliateSvc.OnOrderRefunded})
	// 事件订阅注册（P3-04）：order.paid → 分站利润入账（订单快照 subsite_profit/profit_eligible）
	dp.Register(data.HandlerReg{Consumer: "reseller.settle", Type: events.OrderPaid, Fn: resellerSettleSvc.OnOrderPaid})
	// 事件订阅注册（P1-06 M1b）：order.paid → 自动交付（reserved→used/即删 + 交付记录；幂等由 FulfillOrder 兜底）
	dp.Register(data.HandlerReg{Consumer: "fulfillment.deliver", Type: events.OrderPaid, Fn: fulfillRepo.OnOrderPaid})
	// 事件订阅注册（P3-04）：order.refunded → 分站利润扣回（refund_deduct 负行/负债态）
	dp.Register(data.HandlerReg{Consumer: "reseller.reversal", Type: events.OrderRefunded, Fn: resellerSettleSvc.OnOrderRefunded})
	// 事件订阅注册（P3-01）：order.paid → 积分产生（等级 points_rule；幂等键 points:<orderID>）
	dp.Register(data.HandlerReg{Consumer: "memberlevel.points_earn", Type: events.OrderPaid, Fn: pointsSvc.OnOrderPaid})
	// 事件订阅注册（P2-05）：交易事件 → 通知分发（email/inbox 按模板逐通道投递）
	for _, typ := range notify.SubscribedEvents() {
		t := typ
		dp.Register(data.HandlerReg{
			Consumer: "notify.dispatcher",
			Type:     t,
			Fn:       notifyDisp.HandleEvent,
		})
	}
	var servers []transport.Server
	switch server.RunMode(appMode) {
	case server.ModeAPI:
		servers = []transport.Server{hs, gs, bs}
	case server.ModeWorker:
		servers = []transport.Server{ws, bs}
	default:
		servers = []transport.Server{hs, gs, ws, bs}
	}
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(logger),
		kratos.Server(servers...),
	)
}

func orDev(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}
