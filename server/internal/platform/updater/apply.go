// updater 应用层：rename 舞步（原子替换）+ 状态机 + 回滚 + 健康门（P2-07 T1）。
//
// 磁盘布局（同目录，保证 rename 原子性——跨文件系统 rename 退化为 copy 即拒绝）：
//
//	<dir>/zcard        当前二进制（新版本落位后由 systemd 拉起）
//	<dir>/zcard.prev   上一代（回滚位，仅保留一代）
//	<dir>/zcard.new    下载临时文件（验证通过后 rename 落位，失败即删）
//	<dir>/update.state 状态机文件（pending → ok）
//	<dir>/backups/     更新前备份（二进制+DB，MarkOK 后保留最近 N 代）
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// 状态机取值。
const (
	StatePending = "pending" // 已替换二进制，等待新进程健康门确认
	StateOK      = "ok"      // 更新闭环（或无更新进行中）
)

// ErrNoRollback 无回滚位（update.state 非 pending 且无 zcard.prev）。
var ErrNoRollback = errors.New("updater: 无可回滚版本")

// State 更新状态文件（serve 启动读取；pending 态装配/迁移失败自动回滚）。
type State struct {
	Status     string `json:"status"`
	FromVer    string `json:"from_version"`
	ToVer      string `json:"to_version"`
	AppliedAt  string `json:"applied_at"`
	BackupDir  string `json:"backup_dir,omitempty"`
	RolledBack bool   `json:"rolled_back,omitempty"`
}

// BackupFunc 更新前 DB 备份（cmd 层注入方言实现：SQLite VACUUM INTO /
// mysqldump / pg_dump；返回备份目录）。nil = 跳过（仅告警）。
type BackupFunc func(ctx context.Context, destDir string) error

const (
	newName   = "zcard.new"
	prevName  = "zcard.prev"
	stateName = "update.state"
	backupDir = "backups"
)

// statePath / SidecarPath 状态文件路径（binaryPath 所在目录）。
func statePath(binaryPath string) string {
	return filepath.Join(filepath.Dir(binaryPath), stateName)
}

// LoadState 读取状态（不存在 = 无更新进行中，Status=ok）。
func LoadState(binaryPath string) (*State, error) {
	raw, err := os.ReadFile(statePath(binaryPath))
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Status: StateOK}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		// 损坏状态按无更新处理（fail-open 仅影响回滚位清理，不影响服务）
		return &State{Status: StateOK}, nil
	}
	return &s, nil
}

func saveState(binaryPath string, s *State) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(binaryPath), raw, 0o644)
}

// Apply 原子替换（rename 舞步）：写 .new → chmod → current 改名 .prev →
// .new 落位 current → 写 pending 态。任何一步失败磁盘可恢复（.new 可删，
// 未到 rename 阶段 current 未动）。.new 始终与 current 同目录（同文件系统，
// rename 原子性前提）。
func Apply(binaryPath, fromVer, toVer string, newBin []byte) error {
	dir := filepath.Dir(binaryPath)
	tmp := filepath.Join(dir, newName)
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return fmt.Errorf("updater: 写入临时文件失败: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	prev := filepath.Join(dir, prevName)
	_ = os.Remove(prev) // 上一代残留（上次更新未闭环）直接覆盖
	if err := os.Rename(binaryPath, prev); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("updater: 保留回滚位失败: %w", err)
	}
	if err := os.Rename(tmp, binaryPath); err != nil {
		// 舞步中断：回放 current（.prev 尚在）
		_ = os.Rename(prev, binaryPath)
		return fmt.Errorf("updater: 落位新版本失败: %w", err)
	}
	return saveState(binaryPath, &State{
		Status: StatePending, FromVer: fromVer, ToVer: toVer,
		AppliedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// Rollback 显式回滚：.prev swap 回 current，状态清理。
// 无 .prev 返回 ErrNoRollback（OnFailure 单元随机崩溃不误伤）。
func Rollback(binaryPath string) error {
	dir := filepath.Dir(binaryPath)
	prev := filepath.Join(dir, prevName)
	if _, err := os.Stat(prev); err != nil {
		return ErrNoRollback
	}
	if err := copyOrRemoveBad(binaryPath); err != nil {
		return err
	}
	if err := os.Rename(prev, binaryPath); err != nil {
		return fmt.Errorf("updater: 回滚落位失败: %w", err)
	}
	s, _ := LoadState(binaryPath)
	s.Status = StateOK
	s.RolledBack = true
	return saveState(binaryPath, s)
}

// copyOrRemoveBad 回滚前处理损坏的 current：改名留证（failed-<ts>）便于排查。
func copyOrRemoveBad(binaryPath string) error {
	if _, err := os.Stat(binaryPath); err != nil {
		return nil // current 不存在（启动即崩场景）
	}
	dir := filepath.Dir(binaryPath)
	keep := filepath.Join(dir, "zcard.failed-"+time.Now().UTC().Format("20060102T150405"))
	_ = os.Rename(binaryPath, keep) // 失败即删，不阻塞回滚
	return nil
}

// RollbackOnBootFailure serve 启动失败路径：pending 态才回滚（随机崩溃不动二进制）。
// 返回是否执行了回滚（日志用）。
func RollbackOnBootFailure(binaryPath string) bool {
	s, err := LoadState(binaryPath)
	if err != nil || s.Status != StatePending {
		return false
	}
	if err := Rollback(binaryPath); err != nil {
		return false
	}
	return true
}

// MarkOK 健康门通过：pending → ok，清过期备份（保留最近 keep 代）。
func MarkOK(binaryPath string, keep int) error {
	s, err := LoadState(binaryPath)
	if err != nil {
		return err
	}
	if s.Status != StatePending {
		return nil
	}
	s.Status = StateOK
	if err := saveState(binaryPath, s); err != nil {
		return err
	}
	pruneBackups(filepath.Join(filepath.Dir(binaryPath), backupDir), keep)
	return nil
}

func pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keep {
		return
	}
	// 目录名含时间戳，字典序即时间序
	for _, e := range entries[:len(entries)-keep] {
		if e.IsDir() {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// HealthGate 启动后自检：轮询 /health 直到 database:true 或超时。
// 通过 → MarkOK；超时 → 返回错误（调用方决定回滚/退出）。
func HealthGate(ctx context.Context, healthURL, binaryPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if pingHealth(healthURL) {
			return MarkOK(binaryPath, 3)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("updater: 健康门超时（%s 未就绪，%s）", healthURL, timeout)
}

func pingHealth(url string) bool {
	cli := &http.Client{Timeout: 3 * time.Second}
	resp, err := cli.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var body struct {
		Status struct {
			Server   bool `json:"server"`
			Database bool `json:"database"`
		} `json:"status"`
	}
	return json.NewDecoder(resp.Body).Decode(&body) == nil && body.Status.Database
}

// BackupPath 构造备份目录（pre-<版本>-<ts>；BackupFunc 落盘目标）。
func BackupPath(binaryPath, version string) string {
	return filepath.Join(filepath.Dir(binaryPath), backupDir,
		"pre-"+version+"-"+time.Now().UTC().Format("20060102T150405"))
}

// RestartService 重启服务：systemd 优先（安装态），失败返回错误提示手动重启
// （裸跑/容器态；容器内 self-update 本就被部署矩阵排除）。
// inProcess=true（admin 面内触发）时由调用方直接优雅退出交 systemd，不走本函数。
func RestartService(unit string) error {
	if unit == "" {
		unit = "zcard"
	}
	systemctl := "/bin/systemctl"
	if !fileExists(systemctl) {
		systemctl = "/usr/bin/systemctl"
	}
	if fileExists(systemctl) {
		cmd := exec.Command(systemctl, "restart", unit+".service")
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			_ = out
		}
	}
	return errors.New("updater: 无法自动重启（非 systemd 环境）——请手动重启服务进程")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
