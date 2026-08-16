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
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/admincmd"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
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
	case "install":
		err = runInstall(args)
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
func runServe(args []string) error {
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

	supply.ServerVersion = orDev(Version)
	app, cleanup, err := wireApp(bc.Server, bc.Data, bc.Security, logger)
	if err != nil {
		return err
	}
	defer cleanup()
	return app.Run()
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
func newApp(logger *slog.Logger, hs *khttp.Server, gs *kgrpc.Server, ws *server.WorkerServer, bs *server.BackgroundServer) *kratos.App {
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
