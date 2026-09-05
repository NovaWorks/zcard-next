// 在线更新模块（doc/在线更新方案.md）：更新链 service + admin API。
//
// 分层：platform/updater（引擎）← 本模块（业务编排：源配置/进度/单飞/重启 hook）
// ← adminv1（HTTP 面）+ cmd CLI（self-update 共用 BackupDatabase）。
package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
)

// BackupDatabase 方言感知更新前备份（方案 §7——schema 只前滚，备份是回滚安全网）。
// SQLite VACUUM INTO（WAL 一致快照零外部工具）/ mysqldump / pg_dump；
// 失败即中止更新（fail-closed，不提供跳过——数据安全优先）。
func BackupDatabase(ctx context.Context, dataCfg *conf.Data, destDir string) error {
	if dataCfg == nil || dataCfg.Database == nil || dataCfg.Database.Driver == "" {
		return fmt.Errorf("配置缺 data.database")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	db := dataCfg.Database
	switch db.Driver {
	case "sqlite":
		d, cleanup, err := data.NewData(dataCfg)
		if err != nil {
			return err
		}
		defer cleanup()
		dest := filepath.Join(destDir, "zcard.db")
		if _, err := d.DB.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
			return fmt.Errorf("VACUUM INTO 失败: %w", err)
		}
		return nil
	case "mysql":
		return dumpCommand(ctx, filepath.Join(destDir, "zcard.sql"), "mysqldump", db.Source)
	case "postgres":
		return dumpCommand(ctx, filepath.Join(destDir, "zcard.sql"), "pg_dump", db.Source)
	default:
		return fmt.Errorf("未知方言 %q", db.Driver)
	}
}

// dumpCommand mysqldump/pg_dump 落盘（外部工具缺失给明确指引；执行失败
// 捕获 stderr 回传——pg_dump 的版本不匹配/认证失败等原因必须直达面板，
// 不能只留一个 exit status 1 让用户猜）。
func dumpCommand(ctx context.Context, dest, tool, dsn string) error {
	bin, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s 不在 PATH（%s 后重试）: %w", tool, map[string]string{
			"pg_dump":   "apt install postgresql-client",
			"mysqldump": "apt install default-mysql-client",
		}[tool], err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	var errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, dsn)
	cmd.Stdout = f
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf) // 服务日志留全量，面板收摘要
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		// 常见分类:服务器版本较新——客户端必须同大版本
		if strings.Contains(msg, "server version mismatch") || strings.Contains(msg, "server version") && strings.Contains(msg, "incompatible") {
			return fmt.Errorf("%s 失败: 服务器 PostgreSQL 版本较新，需安装同大版本客户端（如 apt install postgresql-client-16 / postgresql-client-17）: %s", tool, tail(msg, 400))
		}
		if strings.Contains(msg, "password authentication") || strings.Contains(msg, "no password supplied") {
			return fmt.Errorf("%s 失败: 数据库认证失败（检查 config.yaml 的 data.database 连接信息）: %s", tool, tail(msg, 300))
		}
		return fmt.Errorf("%s 失败: %v: %s", tool, err, tail(msg, 400))
	}
	return nil
}

// tail 截尾部 n 字节（pg_dump 错误要点在末尾；防面板超长）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
