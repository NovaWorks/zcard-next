// 在线更新模块（doc/在线更新方案.md）：更新链 service + admin API。
//
// 分层：platform/updater（引擎）← 本模块（业务编排：源配置/进度/单飞/重启 hook）
// ← adminv1（HTTP 面）+ cmd CLI（self-update 共用 BackupDatabase）。
package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

// dumpCommand mysqldump/pg_dump 落盘（外部工具缺失给明确指引，方案 §3）。
func dumpCommand(ctx context.Context, dest, tool, dsn string) error {
	bin, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s 不在 PATH（%s 后重试）: %w", tool, map[string]string{
			"pg_dump":   "apt install postgresql-client",
			"mysqldump": "apt install mysql-client",
		}[tool], err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.CommandContext(ctx, bin, dsn)
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
