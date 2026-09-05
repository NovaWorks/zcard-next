// updater 应用层：rename 舞步（原子替换）+ 状态机 + 回滚 + 健康门（P2-07 T1）。
//
// 磁盘布局（同目录，保证 rename 原子性——跨文件系统 rename 退化为 copy 即拒绝）：
//
//	<dir>/zcard        当前二进制（新版本落位后由进程管理器/exec 拉起，方案 §5）
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
	"strings"
	"syscall"
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

// Apply 原子替换（内存版薄包装——小产物/测试用；生产更新链走 ApplyFile，
// 124MB 大二进制禁止整包进内存，方案 §8）。
func Apply(binaryPath, fromVer, toVer string, newBin []byte) error {
	tmp := filepath.Join(filepath.Dir(binaryPath), newName)
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return fmt.Errorf("updater: 写入临时文件失败: %w", err)
	}
	if err := ApplyFile(binaryPath, fromVer, toVer, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ApplyFile 原子替换（rename 舞步，落盘版）：downloaded 为已通过哈希校验的
// 新二进制文件（调用方直接下载到 <dir>/zcard.new，边写边校验零内存）。
// chmod → current 改名 .prev → downloaded 落位 current → 写 pending 态。
// 任何一步失败磁盘可恢复（downloaded 可删，未到 rename 阶段 current 未动）。
// downloaded 必须与 binaryPath 同目录（同文件系统，rename 原子性前提）。
func ApplyFile(binaryPath, fromVer, toVer, downloaded string) error {
	dir := filepath.Dir(binaryPath)
	if filepath.Dir(downloaded) != dir {
		return fmt.Errorf("updater: 下载临时文件与二进制不同目录（rename 原子性前提）")
	}
	if err := os.Chmod(downloaded, 0o755); err != nil {
		return fmt.Errorf("updater: 临时文件加执行位失败: %w", err)
	}
	prev := filepath.Join(dir, prevName)
	_ = os.Remove(prev) // 上一代残留（上次更新未闭环）直接覆盖
	if err := os.Rename(binaryPath, prev); err != nil {
		return fmt.Errorf("updater: 保留回滚位失败: %w", err)
	}
	if err := os.Rename(downloaded, binaryPath); err != nil {
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
// inProcess=true（admin 面内触发）时由 serve 层重启三分支处理，不走本函数。
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

// CheckDiskSpace 磁盘预检（方案 §8）：dir 所在文件系统可用空间不足 need 即报错
// （124MB 下载 + GB 级 DB 备份，写一半 ENOSPC 远劣于提前拒绝）。
func CheckDiskSpace(dir string, need int64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return fmt.Errorf("updater: 磁盘预检失败: %w", err)
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	if avail < need {
		return fmt.Errorf("updater: 磁盘空间不足（需 %dMB，%s 可用 %dMB）", need>>20, dir, avail>>20)
	}
	return nil
}

// DetectSupervisor 探测进程管理器（方案 §5 重启三分支依据），判据按可靠度排序：
//  1. ZCARD_SUPERVISOR（install.sh unit 显式声明）
//  2. systemd 运行时注入 env（INVOCATION_ID）
//  3. supervisord 标准注入 env（SUPERVISOR_*）
//  4. 父进程链 comm（宝塔「进程守护管理器」等封装不透传标准 env——实测有
//     env 缺失场景；父链出现 supervisord/pm2 是强判据，不可能误报）
//  5. /proc/self/cgroup 含 supervisord（supervisord 被 systemd 托管时子进程
//     同 cgroup 的兜底）
//
// 注意 cgroup 仍不能用于判 systemd——systemd 系统上 nohup 裸跑进程同样落在
// systemd slice 内（误判即「优雅退出等拉起」而实际无人拉起，服务死透）。
// 探测尽力的盲区由 settings system/update 的 supervisor 字段显式覆盖（service 层）。
func DetectSupervisor() string {
	if v := os.Getenv("ZCARD_SUPERVISOR"); v != "" {
		return v
	}
	if os.Getenv("INVOCATION_ID") != "" {
		return "systemd"
	}
	if os.Getenv("SUPERVISOR_ENABLED") == "1" || os.Getenv("SUPERVISOR_SERVER_URL") != "" {
		return "supervisord"
	}
	if v := detectAncestorManager(); v != "" {
		return v
	}
	if cgroup, err := os.ReadFile("/proc/self/cgroup"); err == nil && strings.Contains(string(cgroup), "supervisord") {
		return "supervisord"
	}
	return "none"
}

// detectAncestorManager 向上父进程链找进程管理器（supervisord / PM2 God Daemon /
// 宝塔守护等——按 comm 名匹配；/proc 不可读（非 Linux）即跳过）。
func detectAncestorManager() string {
	pid := os.Getppid()
	for i := 0; i < 12 && pid > 1; i++ {
		if comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
			name := strings.ToLower(strings.TrimSpace(string(comm)))
			switch {
			case strings.Contains(name, "supervisor"):
				return "supervisord"
			case strings.HasPrefix(name, "pm2"), name == "god":
				return "pm2"
			}
		}
		next, ok := parentPID(pid)
		if !ok {
			return ""
		}
		pid = next
	}
	return ""
}

// parentPID /proc/<pid>/stat 的 ppid（comm 含空格时括号定位）。
func parentPID(pid int) (int, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	s := string(raw)
	if idx := strings.LastIndex(s, ")"); idx >= 0 && idx+2 <= len(s) {
		fields := strings.Fields(s[idx+2:])
		if len(fields) >= 2 { // state ppid
			var pp int
			if _, err := fmt.Sscanf(fields[1], "%d", &pp); err == nil {
				return pp, true
			}
		}
	}
	return 0, false
}

// RestartSelf 裸跑降级路径：syscall.Exec 同进程映像替换（方案 §5）。
// PID/父子关系/env/cwd 全保留——systemd 主进程无感、nohup 挂载关系不变；
// 成功不返回；失败则旧进程内存映像仍在（磁盘已换新，返回错误提示手动重启）。
// 调用前必须已完成优雅停机（serve 层 app.Stop 后调用）。
func RestartSelf(binaryPath string) error {
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("updater: exec 目标不存在: %w", err)
	}
	return syscall.Exec(binaryPath, os.Args, os.Environ())
}
