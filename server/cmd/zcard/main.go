// zcard 入口（规划 / –10.3）。
//
// 用法：
//
//	zcard serve [-conf configs] [-mode all|api|worker] # 启动服务（默认 all）
//	zcard migrate [-conf configs] # 应用待执行迁移后退出
//	zcard admin create|list|reset-password ... # 运维子命令（同二进制，容器免工具）
//	zcard install # 安装向导（）
//	zcard version # 版本信息
//
// 运行模式：api 与 worker 共享全部代码，靠 -mode 选择装配的 server（worker 模式
// 的 asynq 消费者与周期任务 随交易闭环交付； 仅 api/all）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/admincmd"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/affiliate"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/reseller"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
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
	case "dbtest":
		err = runDBTest(args)
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

// newLogger slog 构造（text/json + 级别；结构化：事件名 + 关键 ID，）。
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

// applyMigrationsIfEnabled 启动迁移（：加锁串行，失败拒绝启动）。
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

// completePendingInstall 在线安装接力（库切换重启后）：待装凭据存在 → 新库安装。
func completePendingInstall(ctx context.Context, bc *conf.Bootstrap) error {
	d, cleanup, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer cleanup()
	return settings.CompletePendingInstall(ctx, d)
}

// ensureSeeds 启动补种（幂等；独立于迁移开关——表已存在即可）：基础货币
// 缺失时写入（老库升级自动补 CNY，新库由 Install 事务同源写入）。
func ensureSeeds(ctx context.Context, bc *conf.Bootstrap) error {
	d, cleanup, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer cleanup()
	return settings.EnsureDefaultCurrencies(ctx, d)
}

// runServe 启动服务。
func runServe(args []string) (err error) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	mode := fs.String("mode", "all", "运行模式 all|api|worker")
	if err := fs.Parse(args); err != nil {
		return err
	}
	settings.SetConfDir(*confDir)
	// 零配置首启：配置缺失时生成 SQLite 引导配置（仅作向导载体，Web 里再选库）
	if created, cerr := ensureBootstrapConfig(*confDir); cerr != nil {
		return cerr
	} else if created {
		abs, _ := filepath.Abs(*confDir)
		fmt.Printf("zcard: 未发现配置，已生成引导配置 %s/config.yaml（0600，含随机密钥）\n", abs)
		fmt.Printf("zcard: 浏览器打开 http://<本机IP>:8000/install 选择数据库（PG 推荐/MySQL/SQLite）完成安装\n")
	}
	bc, err := loadBootstrap(*confDir)
	if err != nil {
		return err
	}
	ensureSQLiteDir(bc)
	logger := newLogger(bc)
	log.SetDefault(logger)

	// 更新 pending 态：serve 任何启动失败（配置/迁移/装配）自动回滚二进制，
	// systemd 重启即旧版本（ 自动回滚；DB 只前滚，靠备份恢复）。
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
	// 在线安装·库切换接力：待装凭据存在 → 在新库上补装（迁移已完成）
	if err := completePendingInstall(context.Background(), bc); err != nil {
		return err
	}
	if err := ensureSeeds(context.Background(), bc); err != nil {
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
	settings.SetServerVersion(orDev(Version))
	server.Version = orDev(Version) // /health 下发真实构建版本（此前恒 dev 的根因）
	deps, cleanup, err := wireApp(bc.Server, bc.Data, bc.Security, logger)
	if err != nil {
		return err
	}
	app, updateSvc := deps.App, deps.Update
	// 多进程形态禁用面板更新（方案 ）：api/worker 分进程会撞 update.state，
	// 且面板只重启 api 进程 → worker 旧代码 + 新 schema。滚动更新走 CLI。
	if appMode != string(server.ModeAll) && updateSvc != nil {
		updateSvc.Disable(fmt.Sprintf("多进程模式（%s）面板在线更新已禁用——请用 zcard self-update 逐进程滚动", appMode))
	}
	// cleanup 单次化：重启路径 exec 前显式收口，进程退出路径 defer 兜底
	var cleanupOnce sync.Once
	doCleanup := func() { cleanupOnce.Do(cleanup) }
	defer doCleanup()

	// 更新重启 hook（三分支）：置位标记 + 异步优雅停机；
	// app.Run 返回后按 supervisor 分流——管理器环境 exit 0 交拉起，裸跑 exec 自替换
	var restarting atomic.Bool
	var supAtRestart atomic.Value // 停机前预取的 supervisor 判定（doCleanup 关 DB 后配置读不到）
	supervisorOf := func() string {
		if v := supAtRestart.Load(); v != nil {
			return v.(string)
		}
		s := updater.DetectSupervisor()
		if updateSvc != nil {
			s = updateSvc.SupervisorKind(context.Background())
		}
		return s
	}
	if updateSvc != nil {
		updateSvc.SetRestartFn(func() {
			supAtRestart.Store(supervisorOf()) // 停机前预取（此刻 DB 仍活）
			restarting.Store(true)
			go func() {
				// kratos App.Stop 无参（内部 5s 优雅停机）；hook 只负责触发
				app.Stop()
			}()
		})
	}

	// 更新健康门（）：HTTP 就绪 + DB 连通自检通过 → pending 转 ok；
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
	runErr := app.Run()
	if restarting.Load() {
		doCleanup() // exec 前显式收口（exec 不走 defer 链）
		if err := restartAfterUpdate(binPath, logger, supervisorOf()); err != nil {
			// exec 失败：旧进程内存映像仍在服务，磁盘已是新版——复位更新态允许重试，
			// 返回 nil 保持 exit 0（非零会误触 systemd OnFailure 回滚掉刚落位的新版）
			logger.Error("update.restart.failed", "error", err)
			if updateSvc != nil {
				updateSvc.OnRestartFailed(err)
			}
		}
		return nil
	}
	return runErr
}

// restartAfterUpdate 更新后重启分流（方案 ）：仅 systemd 走 exit 0 交拉起
// （Restart=always 由 install.sh 保证）；supervisord/宝塔/pm2/裸跑一律 exec 同进程
// 替换（PID 不变管理器零感知，规避 autorestart 配置不可控）。sup 由调用方在
// 停机前预取（doCleanup 关 DB 后配置读不到，回落探测会产生意图漂移）。
func restartAfterUpdate(binPath string, logger *slog.Logger, sup string) error {
	if binPath == "" {
		return fmt.Errorf("无法定位二进制路径")
	}
	// 仅 systemd 走「exit 0 交拉起」——Restart=always 由 install.sh 保证。
	// supervisord/宝塔守护的 autorestart 配置不可控（unexpected 语义下 exit 0
	// 不触发重启 = 更新即停服），pm2 同理——一律 exec 同进程替换（PID 不变，
	// 管理器零感知，对两者完全兼容）。
	if sup == "systemd" {
		logger.Info("update.restart.exit_to_supervisor", "supervisor", sup)
		return nil
	}
	logger.Info("update.restart.exec_self", "supervisor", sup, "bin", binPath)
	return updater.RestartSelf(binPath)
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

// runDBTest 安装脚本配套：目标库/Redis 连接校验（库不存在自动创建；成功 exit 0）。
func runDBTest(args []string) error {
	fs := flag.NewFlagSet("dbtest", flag.ExitOnError)
	dialect := fs.String("dialect", "", "postgres | mysql")
	host := fs.String("host", "127.0.0.1", "数据库主机")
	port := fs.Int("port", 0, "数据库端口（0=按方言默认 5432/3306）")
	user := fs.String("user", "", "数据库用户")
	password := fs.String("password", "", "数据库密码")
	name := fs.String("name", "", "数据库名")
	redisAddr := fs.String("redis", "", "Redis 地址（mysql/postgres 必填）")
	redisPassword := fs.String("redis-password", "", "Redis 密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *port == 0 {
		if *dialect == "mysql" {
			*port = 3306
		} else {
			*port = 5432
		}
	}
	if err := settings.ValidateSwitchInput(*dialect, *host, int32(*port), *user, *password, *name, *redisAddr, *redisPassword); err != nil {
		return err
	}
	fmt.Println("连接成功：数据库与 Redis 验证通过（库已就绪）")
	return nil
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

// runInstall 安装向导（：CLI 交互式；Web /install 前端补齐）。
func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := ensureBootstrapConfig(*confDir); err != nil {
		return err
	}
	bc, err := loadBootstrap(*confDir)
	if err != nil {
		return err
	}
	ensureSQLiteDir(bc)
	// 全新库先迁移（serve 启动链同款）——否则 CLI 直装报 no such table
	if err := applyMigrationsIfEnabled(context.Background(), bc); err != nil {
		return err
	}
	d, cleanup, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := settings.RunInstallCLI(context.Background(), d); err != nil {
		return err
	}
	fmt.Println("安装完成：浏览器打开服务地址即可使用（后台 /admin）")
	return nil
}

// newApp kratos.App 装配：按模式选择 server 组合（规划 单进程多角色）。
//
//	all = HTTP + gRPC + worker + 后台（默认，单机形态）
//	api = HTTP + gRPC + 后台relay（多实例 api，cron 不注册）
//	worker = worker + 后台（消费与周期任务，多实例 asynq 竞争消费）
func newApp(logger *slog.Logger, hs *khttp.Server, gs *kgrpc.Server, ws *server.WorkerServer, bs *server.BackgroundServer, dp *data.Dispatcher, procureSvc *procurement.ProcureService, notifyDisp *notify.Dispatcher, affiliateSvc *affiliate.AffiliateService, resellerSettleSvc *reseller.SettleService, fulfillRepo *fulfillment.DeliveryRepoImpl, pointsSvc *memberlevel.PointsService, orderUC *order.OrderUsecase, payRepo *payment.PaymentRepoImpl, walletRepo *wallet.WalletRepoImpl, stockGate orderport.UpstreamStockGate) *kratos.App {
	// 破环点：order 超时取消慢通道顺延探测 ← payment 实现
	// （wire 环 OrderUsecase ↔ PaymentRepoImpl，装配期手工注入——同 dp.Register 模式）
	orderUC.SetSlowPaymentChecker(payRepo)
	// 破环点：上游代发项下单前实时库存预检 ← supply 网关实现
	orderUC.SetStockGate(stockGate)
	// 佣金提现打通：打款 FIFO 消耗佣金（affiliate → wallet 注入，装配期一次）
	walletRepo.SetCommissionConsumer(affiliateSvc.Repo())
	// 事件订阅注册（）：order.paid → 采购（wire 破环点，见 bootstrap/queue.go 注释）
	dp.Register(data.HandlerReg{
		Consumer: "procurement.order_paid",
		Type:     events.OrderPaid,
		Fn:       procureSvc.OnOrderPaid,
	})
	// 事件订阅注册（）：order.paid → 三级佣金入账；order.refunded → 逆向扣回
	dp.Register(data.HandlerReg{Consumer: "affiliate.settle", Type: events.OrderPaid, Fn: affiliateSvc.OnOrderPaid})
	dp.Register(data.HandlerReg{Consumer: "affiliate.reversal", Type: events.OrderRefunded, Fn: affiliateSvc.OnOrderRefunded})
	// 事件订阅注册（）：order.paid → 分站利润入账（订单快照 subsite_profit/profit_eligible）
	dp.Register(data.HandlerReg{Consumer: "reseller.settle", Type: events.OrderPaid, Fn: resellerSettleSvc.OnOrderPaid})
	// 事件订阅注册（ ）：order.paid → 自动交付（reserved→used/即删 + 交付记录；幂等由 FulfillOrder 兜底）
	dp.Register(data.HandlerReg{Consumer: "fulfillment.deliver", Type: events.OrderPaid, Fn: fulfillRepo.OnOrderPaid})
	// 事件订阅注册（）：order.refunded → 分站利润扣回（refund_deduct 负行/负债态）
	dp.Register(data.HandlerReg{Consumer: "reseller.reversal", Type: events.OrderRefunded, Fn: resellerSettleSvc.OnOrderRefunded})
	// 事件订阅注册（）：order.paid → 积分产生（等级 points_rule；幂等键 points:<orderID>）
	dp.Register(data.HandlerReg{Consumer: "memberlevel.points_earn", Type: events.OrderPaid, Fn: pointsSvc.OnOrderPaid})
	// 事件订阅注册（）：交易事件 → 通知分发（email/inbox 按模板逐通道投递）
	for _, typ := range notify.SubscribedEvents() {
		t := typ
		dp.Register(data.HandlerReg{
			Consumer: "notify.dispatcher",
			Type:     t,
			Fn:       notifyDisp.HandleEvent,
		})
	}
	var servers []transport.Server
	// gRPC 可选（配置 addr 为空时不装配）
	withGRPC := func(list []transport.Server) []transport.Server {
		if gs == nil {
			return list
		}
		return append([]transport.Server{gs}, list...)
	}
	switch server.RunMode(appMode) {
	case server.ModeAPI:
		servers = withGRPC([]transport.Server{hs, bs})
	case server.ModeWorker:
		servers = []transport.Server{ws, bs}
	default:
		servers = withGRPC([]transport.Server{hs, ws, bs})
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
