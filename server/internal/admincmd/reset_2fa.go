package admincmd

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/encoding"
	"github.com/go-sql-driver/mysql"
)

func runReset2FA(args []string) error {
	fs := flag.NewFlagSet("admin reset-2fa", flag.ContinueOnError)
	confDir := fs.String("conf", "configs", "配置目录（建议绝对路径）")
	username := fs.String("username", "", "管理员登录名")
	yes := fs.Bool("yes", false, "跳过交互确认")
	dry := fs.Bool("dry-run", false, "只检查目标，不修改")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return fmt.Errorf("--username 必填")
	}
	path, err := filepath.Abs(*confDir)
	if err != nil {
		return err
	}
	bc := &conf.Bootstrap{}
	if err = scanRecoveryConf(path, bc); err != nil {
		return err
	}
	if bc.Data == nil || bc.Data.Database == nil {
		return fmt.Errorf("缺少数据库配置")
	}
	cfg := bc.Data.Database
	target := "配置中的数据库"
	if cfg.Driver == "mysql" {
		if parsed, e := mysql.ParseDSN(cfg.Source); e == nil {
			target = parsed.Addr + "/" + parsed.DBName
		}
	}
	if cfg.Driver == "postgres" || cfg.Driver == "pgx" {
		if parsed, e := url.Parse(cfg.Source); e == nil && parsed.Host != "" {
			target = parsed.Host + parsed.Path
		}
	}
	// Refuse SQLite implicit creation: a mistyped working directory must never
	// look like successful recovery against an empty installation.
	if strings.HasPrefix(cfg.Driver, "sqlite") {
		source := strings.SplitN(strings.TrimPrefix(cfg.Source, "file:"), "?", 2)[0]
		source, err = url.PathUnescape(source)
		if err != nil {
			return err
		}
		if source == "" || source == ":memory:" || strings.Contains(cfg.Source, "mode=memory") {
			return fmt.Errorf("恢复命令不支持内存数据库")
		}
		target, _ = filepath.Abs(source)
		info, err := os.Stat(source)
		if err != nil || info.IsDir() {
			return fmt.Errorf("SQLite 数据库文件不存在或不可读，请确认工作目录和配置")
		}
	}
	d, cleanup, err := data.NewData(bc.Data)
	if err != nil {
		return fmt.Errorf("无法打开目标数据库，请检查配置")
	}
	defer cleanup()
	ctx := context.Background()
	u, err := d.Client.AdminUser.Query().Where(adminuser.Username(*username)).Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("管理员 %q 不存在", *username)
	}
	if err != nil {
		return fmt.Errorf("读取管理员失败，请确认数据库已经完成本版本迁移: %w", err)
	}
	fmt.Printf("配置：%s\n数据库类型：%s\n目标：%s\n账号：%s（ID %d，启用 %v，已绑定 %v）\n", path, cfg.Driver, target, u.Username, u.ID, u.Enabled, len(u.TotpSecret) > 0)
	if *dry {
		fmt.Println("检查完成，未修改任何数据")
		return nil
	}
	if !*yes {
		fmt.Printf("将解除两步验证并注销该账号全部后台会话。请输入账号 %s 确认：", u.Username)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("未收到确认；自动化执行请明确传入 --yes")
		}
		if strings.TrimSpace(line) != u.Username {
			return fmt.Errorf("确认不匹配，未修改")
		}
	}
	if err := identity.ResetAdminTwoFactor(ctx, d, u.ID, 0, "server CLI recovery"); err != nil {
		return fmt.Errorf("重置失败，未完成恢复: %w", err)
	}
	fmt.Printf("已重置 %s 的两步验证，待确认绑定、恢复码及旧登录凭证已失效。密码、权限和禁用状态保持不变。\n", u.Username)
	return nil
}

// The standard decoder logs raw values on errors. Recovery is often invoked
// with a mistyped path; never print database files or malformed secret configs.
func scanRecoveryConf(path string, out *conf.Bootstrap) error {
	c := config.New(config.WithSource(file.NewSource(path)), config.WithDecoder(func(src *config.KeyValue, target map[string]any) error {
		if codec := encoding.GetCodec(src.Format); codec != nil {
			if err := codec.Unmarshal(src.Value, &target); err == nil {
				return nil
			}
		}
		src.Value = []byte("[redacted]")
		return fmt.Errorf("配置文件格式无效，请检查 --conf")
	}))
	defer c.Close()
	if err := c.Load(); err != nil {
		return fmt.Errorf("无法读取配置，请确认 --conf 指向正确的配置目录或文件")
	}
	if err := c.Scan(out); err != nil {
		return fmt.Errorf("配置字段无效，请检查数据库配置")
	}
	return nil
}
