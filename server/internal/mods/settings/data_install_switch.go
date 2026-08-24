package settings

// 在线安装·数据库切换（安装向导选择 mysql/postgres 时）：
//   校验连接（库不存在自动建）→ 写 <confDir>/database.yaml 覆盖配置（data 段）
//   → 写待安装凭据 data/.install-pending.json（0600，装完即删）→ 自重启
//   → 新库启动链（迁移 → CompletePendingInstall 补装管理员/种子 → 正常服务）。
//
// SQLite 路径不落盘任何文件——当前库直装（零依赖体验不变）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/redis/go-redis/v9"
)

// confDir 在线安装写覆盖配置的目标目录（runServe 注入，-conf 值）。
var confDir = "configs"

// SetConfDir 注入配置目录（main runServe 调用）。
func SetConfDir(dir string) {
	if dir != "" {
		confDir = dir
	}
}

// pendingInstallFile 待安装凭据落点（工作目录 data/ 下——SQLite 同址，重启后可寻）。
func pendingInstallFile() string { return filepath.Join("data", ".install-pending.json") }

// overrideConfigFile 数据库覆盖配置（Kratos 目录合并：database.yaml 字典序在
// config.yaml 之后，同名键覆盖——仅覆盖 data 段，其余配置不动）。
func overrideConfigFile() string { return filepath.Join(confDir, "database.yaml") }

// dbSwitchInput 库切换参数（向导收集）。
type dbSwitchInput struct {
	Dialect       string
	Host          string
	Port          int32
	User          string
	Password      string
	Name          string
	RedisAddr     string
	RedisPassword string
}

// ValidateSwitchInput 校验目标库/Redis（安装脚本 dbtest 子命令复用；库不存在自动创建）。
func ValidateSwitchInput(dialect, host string, port int32, user, password, name, redisAddr, redisPassword string) error {
	return validateSwitch(context.Background(), dbSwitchInput{
		Dialect: dialect, Host: host, Port: port, User: user, Password: password,
		Name: name, RedisAddr: redisAddr, RedisPassword: redisPassword,
	})
}

// buildDSN 按方言拼 DSN（与 config.example 口径一致）。
func buildDSN(in dbSwitchInput) (string, error) {
	switch in.Dialect {
	case "mysql":
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=True&loc=UTC&charset=utf8mb4",
			in.User, in.Password, in.Host, in.Port, in.Name), nil
	case "postgres":
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(in.User, in.Password),
			Host:     fmt.Sprintf("%s:%d", in.Host, in.Port),
			Path:     "/" + in.Name,
			RawQuery: "sslmode=disable",
		}
		return u.String(), nil
	}
	return "", fmt.Errorf("不支持的方言 %q", in.Dialect)
}

// validateSwitch 测试目标库 + Redis 连通（库不存在自动创建；库名缺失/凭据错误
// 返回可读错误——向导「测试连接」与正式安装共用）。
func validateSwitch(ctx context.Context, in dbSwitchInput) error {
	if in.Host == "" || in.Port <= 0 || in.User == "" || in.Name == "" {
		return fmt.Errorf("数据库主机/端口/用户/库名不能为空")
	}
	if in.RedisAddr == "" {
		return fmt.Errorf("选择 MySQL/PostgreSQL 必须配置 Redis（SQLite 模式可省略）")
	}
	// Redis 连通
	rdb := redis.NewClient(&redis.Options{
		Addr: in.RedisAddr, Password: in.RedisPassword,
		DialTimeout: 3 * time.Second,
	})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis 连接失败（%s）: %v", in.RedisAddr, err)
	}
	// 数据库：先连服务级（无库名）→ 建库（不存在时）→ 再按 DSN 验证
	switch in.Dialect {
	case "mysql":
		server := fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=True&loc=UTC",
			in.User, in.Password, in.Host, in.Port)
		db, err := sql.Open("mysql", server)
		if err != nil {
			return fmt.Errorf("MySQL 连接失败: %v", err)
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci", in.Name)); err != nil {
			return fmt.Errorf("创建数据库失败（检查账号权限）: %v", err)
		}
	case "postgres":
		u := url.URL{Scheme: "postgres", User: url.UserPassword(in.User, in.Password),
			Host: fmt.Sprintf("%s:%d", in.Host, in.Port), Path: "/postgres", RawQuery: "sslmode=disable"}
		db, err := sql.Open("postgres", u.String())
		if err != nil {
			return fmt.Errorf("PostgreSQL 连接失败: %v", err)
		}
		defer db.Close()
		var exists bool
		if err := db.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", in.Name).Scan(&exists); err != nil {
			return fmt.Errorf("查询库失败: %v", err)
		}
		if !exists {
			// PG 建库不支持参数占位（标识符）
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, in.Name)); err != nil {
				return fmt.Errorf("创建数据库失败（检查账号权限）: %v", err)
			}
		}
	default:
		return fmt.Errorf("不支持的方言 %q", in.Dialect)
	}
	// 按最终 DSN 复验（含库名）
	dsn, _ := buildDSN(in)
	db, err := sql.Open(in.Dialect, dsn)
	if err != nil {
		return fmt.Errorf("数据库连接失败: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("数据库连接失败: %v", err)
	}
	// 建表权限探测：能连 ≠ 能建表（PG 15+ 默认 public schema 不开放 CREATE，
	// MySQL 用户也可能无 CREATE）——早报错优于切库自重启后迁移失败
	return probeCreateTable(ctx, db, in.Dialect, in.User, in.Name)
}

// probeCreateTable 建表权限探测：建一张随机名临时表再删除（不留痕迹）。
// 失败返回带授权指引的错误——「测试连接」阶段即暴露权限问题。
func probeCreateTable(ctx context.Context, db *sql.DB, dialect, user, dbName string) error {
	name := fmt.Sprintf("zcard_perm_probe_%d", time.Now().UnixNano())
	var create, drop string
	switch dialect {
	case "postgres":
		create = fmt.Sprintf(`CREATE TABLE %s (id bigint)`, name)
		drop = fmt.Sprintf(`DROP TABLE %s`, name)
	case "mysql":
		create = fmt.Sprintf("CREATE TABLE `%s` (`id` bigint)", name)
		drop = fmt.Sprintf("DROP TABLE `%s`", name)
	default:
		return nil
	}
	if _, err := db.ExecContext(ctx, create); err != nil {
		if strings.Contains(err.Error(), "denied") || strings.Contains(err.Error(), "permission") {
			var hint string
			if dialect == "postgres" {
				hint = fmt.Sprintf("GRANT ALL ON SCHEMA public TO \"%s\"", user)
			} else {
				hint = fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'", dbName, user)
			}
			return fmt.Errorf("数据库账号无建表权限（%v）——请使用数据库超级用户/owner 账号，或执行授权：%s", err, hint)
		}
		return fmt.Errorf("建表权限探测失败: %v", err)
	}
	_, _ = db.ExecContext(ctx, drop)
	return nil
}

// writeSwitchFiles 落盘覆盖配置 + 待安装凭据。
func writeSwitchFiles(in dbSwitchInput, admin InstallInput) error {
	dsn, err := buildDSN(in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return err
	}
	// 覆盖配置（仅 data 段；密码含特殊字符走 yaml 双引号转义）
	override := fmt.Sprintf(`# 在线安装生成（数据库切换）——重装可删除本文件
data:
  database:
    driver: %s
    source: %q
  redis:
    addr: %s
    password: %q
`, in.Dialect, dsn, in.RedisAddr, in.RedisPassword)
	if err := os.WriteFile(overrideConfigFile(), []byte(override), 0o644); err != nil {
		return fmt.Errorf("写覆盖配置失败: %w", err)
	}
	// 待安装凭据（新库启动后补装；装完即删）
	if err := os.MkdirAll(filepath.Dir(pendingInstallFile()), 0o755); err != nil {
		return err
	}
	payload, _ := json.Marshal(admin)
	if err := os.WriteFile(pendingInstallFile(), payload, 0o600); err != nil {
		return fmt.Errorf("写待安装凭据失败: %w", err)
	}
	return nil
}

// scheduleSelfRestart 延迟自重启（syscall.Exec 原位换进程：保 pid/端口，
// systemd 与前台运行均适用；延迟让安装响应先送达客户端）。
func scheduleSelfRestart(delay time.Duration) {
	time.AfterFunc(delay, func() {
		exe, err := os.Executable()
		if err != nil {
			exe = os.Args[0]
		}
		_ = syscall.Exec(exe, os.Args, os.Environ())
	})
}

// CompletePendingInstall 启动链接力（runServe 在迁移后调用）：存在待安装凭据
// 且当前库未安装 → 在新库上执行安装并删除凭据文件。
func CompletePendingInstall(ctx context.Context, d *data.Data) error {
	raw, err := os.ReadFile(pendingInstallFile())
	if err != nil {
		return nil // 无待装（正常启动）
	}
	var in InstallInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("待安装凭据损坏（可删除 %s 后重新安装）: %w", pendingInstallFile(), err)
	}
	if Installed(ctx, d) {
		return os.Remove(pendingInstallFile()) // 新库已装过（幂等）：只清凭据
	}
	if err := Install(ctx, d, in); err != nil {
		return fmt.Errorf("补装失败: %w", err)
	}
	return os.Remove(pendingInstallFile())
}
