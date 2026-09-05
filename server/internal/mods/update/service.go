// 更新链 service（doc/在线更新方案.md §9）：状态机 + 单飞 + 进度回调 + 重启 hook。
//
// 重启分工：本模块落位二进制后调用 restartFn（serve 层注入——优雅停机 +
// 三分支：进程管理器拉起 / 裸跑 syscall.Exec）；新进程经 update.state pending
// 健康门确认（updater.HealthGate，main 装配）。
package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/platform/updater"
)

// 阶段取值（前端轮询协议）。
const (
	PhaseIdle        = "idle"
	PhaseChecking    = "checking"
	PhaseBackingUp   = "backing_up"
	PhaseDownloading = "downloading"
	PhaseApplying    = "applying"
	PhaseRestarting  = "restarting"
	PhaseVerifying   = "verifying"   // 新进程健康门中（磁盘 state pending 合成）
	PhaseRolledBack  = "rolled_back" // 最近一次结果为回滚
	PhaseFailed      = "failed"
)

// ErrBusy 更新链进行中（apply 单飞互斥）。
var ErrBusy = errors.New("update: 更新正在进行中")

// ErrDisabled 面板更新被禁用（多进程模式，方案 §12——api/worker 分进程会撞
// update.state 且 worker 跑旧代码+新 schema，须走 CLI 滚动）。
var ErrDisabled = errors.New("update: 当前为多进程部署，面板在线更新已禁用（请用 zcard self-update 滚动各进程）")

// probeCacheTTL auto 源探测缓存（方案 §4.3：10 分钟）。
const probeCacheTTL = 10 * time.Minute

// Status 状态快照（admin API 下发；Current/Supervisor 每次现算——重启后即新值）。
type Status struct {
	Phase       string
	Current     string
	Target      string
	Progress    int32
	Err         string
	Source      string // 生效源展示（github | <accel> | static:<base>）
	Mode        string // 配置模式
	Supervisor  string
	HasUpdate   bool
	Notes       string
	Latest      string
	CheckedAt   time.Time
	BackupDir   string
	Busy        bool
	History     []updater.ReleaseNote // 历史版本 changelog（manifest 权威源）
	BackupReady bool                  // 备份工具就绪（pg_dump/mysqldump 按方言；缺失则更新会被 fail-closed 中止）
	BackupHint  string                // 缺失时的安装指引（事前警示，不等更新失败才报）
}

// Service 更新编排。
type Service struct {
	mu       sync.Mutex
	busy     bool
	st       Status
	probe    *updater.ProbeOutcome
	probedAt time.Time

	dataCfg   *conf.Data
	uc        *settings.SettingsUsecase
	binPath   string
	restartFn func()
	disabled  string // 非空=禁用原因（多进程模式）
}

// NewService wire 构造。
func NewService(dataCfg *conf.Data, uc *settings.SettingsUsecase) *Service {
	bin := ""
	if p, err := os.Executable(); err == nil {
		if rp, err2 := filepath.EvalSymlinks(p); err2 == nil {
			bin = rp
		} else {
			bin = p
		}
	}
	return &Service{dataCfg: dataCfg, uc: uc, binPath: bin, st: Status{Phase: PhaseIdle}}
}

// SetRestartFn serve 层注入重启回调（优雅停机 + 三分支；nil=禁用重启——测试态）。
func (s *Service) SetRestartFn(f func()) { s.restartFn = f }

// Disable 禁用面板更新链（serve 层注入：-mode != all 时调用，方案 §12）。
func (s *Service) Disable(reason string) { s.disabled = reason }

// OnRestartFailed 重启失败复位（serve 层 exec 失败分支调用）：新版本已落盘但进程
// 未能重启——恢复 busy 允许重试，phase 转 failed 附手动指引（磁盘已是新版）。
func (s *Service) OnRestartFailed(err error) {
	s.mu.Lock()
	s.busy = false
	s.st.Phase = PhaseFailed
	s.st.Err = fmt.Sprintf("重启失败（新版本已落盘，请手动重启服务生效）: %v", err)
	s.mu.Unlock()
}

// DisabledErr 禁用态错误（空=启用）。
func (s *Service) DisabledErr() error {
	if s.disabled == "" {
		return nil
	}
	return ErrDisabled
}

// sourceConfig settings system/update → updater.SourceConfig。
func (s *Service) sourceConfig(ctx context.Context) updater.SourceConfig {
	var cfg updater.SourceConfig
	if s.uc != nil {
		_ = settings.GetStruct(ctx, s.uc, "system", "update", &cfg)
	}
	return cfg.Normalize()
}

// resolveClient 源解析（auto 结果缓存 10 分钟）。
func (s *Service) resolveClient(ctx context.Context, cfg updater.SourceConfig) (*updater.Client, *updater.ProbeOutcome, error) {
	if cfg.Mode != "auto" {
		return updater.ResolveSource(ctx, cfg, 5*time.Second) // 钉死模式无网络探测
	}
	s.mu.Lock()
	if s.probe != nil && time.Since(s.probedAt) < probeCacheTTL {
		out := s.probe
		s.mu.Unlock()
		c := updater.NewClient(out.Mode, cfg.Repo, out.Accel, cfg.StaticBase)
		return c, out, nil
	}
	s.mu.Unlock()
	c, out, err := updater.ResolveSource(ctx, cfg, 5*time.Second)
	if err != nil {
		// 探测失败清缓存：auto 判定可能基于过期网络快照，下轮重探
		s.mu.Lock()
		s.probe = nil
		s.mu.Unlock()
		return nil, nil, err
	}
	s.mu.Lock()
	s.probe, s.probedAt = out, time.Now()
	s.mu.Unlock()
	return c, out, nil
}

// CheckResult 检查结果。
type CheckResult struct {
	Current   string
	Latest    string
	HasUpdate bool
	Notes     string
	Channel   string
	Source    string
	History   []updater.ReleaseNote // 历史版本 changelog（manifest 权威源）
}

// Check 手动检查（源解析 + manifest 验签；结果缓存进 status 供弹窗展示）。
func (s *Service) Check(ctx context.Context) (*CheckResult, error) {
	s.setPhase(PhaseChecking)
	defer func() {
		s.mu.Lock()
		p := s.st.Phase
		s.mu.Unlock()
		if p == PhaseChecking {
			s.setPhase(PhaseIdle)
		}
	}()

	cfg := s.sourceConfig(ctx)
	cli, outcome, err := s.resolveClient(ctx, cfg)
	if err != nil {
		s.fail(fmt.Errorf("源解析失败: %w", err))
		return nil, err
	}
	pub, err := updater.PublicKey(updater.DefaultPublicKeyHex)
	if err != nil {
		return nil, err
	}
	m, _, err := cli.FetchManifest(ctx, cfg.Channel, pub)
	if err != nil {
		s.fail(fmt.Errorf("获取清单失败: %w", err))
		return nil, err
	}
	res := &CheckResult{
		Current: cur(), Latest: m.Version,
		HasUpdate: updater.CompareSemver(m.Version, cur()) > 0,
		Notes:     m.Notes, Channel: m.Channel, Source: outcome.SourceDesc(),
		History: m.History,
	}
	s.mu.Lock()
	s.st.HasUpdate, s.st.Notes, s.st.Latest = res.HasUpdate, res.Notes, res.Latest
	s.st.Source, s.st.CheckedAt = res.Source, time.Now()
	s.st.History = res.History
	s.mu.Unlock()
	return res, nil
}

// Apply 触发更新（后台 goroutine 执行链；重复调用 ErrBusy）。
// 链：磁盘预检 → DB 备份 → 落盘下载（进度）→ 原子替换 → 重启 hook。
func (s *Service) Apply(ctx context.Context) error {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	s.mu.Unlock()
	go s.run()
	return nil
}

func (s *Service) run() {
	// 独立 ctx：apply 接口即时返回，请求 ctx 不能带入后台链
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	fail := func(err error) {
		s.fail(err)
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}
	if s.binPath == "" {
		fail(errors.New("无法定位当前二进制"))
		return
	}

	cfg := s.sourceConfig(ctx)
	cli, outcome, err := s.resolveClient(ctx, cfg)
	if err != nil {
		fail(fmt.Errorf("源解析失败: %w", err))
		return
	}
	pub, err := updater.PublicKey(updater.DefaultPublicKeyHex)
	if err != nil {
		fail(err)
		return
	}
	m, _, err := cli.FetchManifest(ctx, cfg.Channel, pub)
	if err != nil {
		fail(fmt.Errorf("获取清单失败: %w", err))
		return
	}
	// 版本单调（防降级攻击；CLI 同款约束）
	if !updater.IsSemver(m.Version) || updater.CompareSemver(m.Version, cur()) <= 0 {
		fail(fmt.Errorf("%w（当前 %s ↔ 目标 %s）", updater.ErrRefuseUpdate, cur(), m.Version))
		return
	}
	assetName := "zcard-" + runtime.GOOS + "-" + runtime.GOARCH
	var assetSize int64
	for _, f := range m.Files {
		if f.Name == assetName {
			assetSize = f.Size
		}
	}
	dir := filepath.Dir(s.binPath)
	// 磁盘预检（方案 §8）：产物 + 64MB 余量
	if err := updater.CheckDiskSpace(dir, assetSize+64<<20); err != nil {
		fail(err)
		return
	}

	// 备份（DB + 旧二进制；失败中止——安全网优先）
	s.setPhase(PhaseBackingUp)
	backupDir := updater.BackupPath(s.binPath, cur())
	if err := BackupDatabase(ctx, s.dataCfg, backupDir); err != nil {
		fail(fmt.Errorf("备份失败，中止更新: %w", err))
		return
	}
	_ = copyBinary(s.binPath, filepath.Join(backupDir, "zcard.old"))

	// 落盘下载（流式哈希；进度回调）。源失败自动换下一个候选从头重试（方案 §4.3：
	// 首选源（auto 探测/钉死）→ 其余加速器逐个兜底，beta 通道排除加速器）
	s.mu.Lock()
	s.st.Phase, s.st.Progress, s.st.BackupDir = PhaseDownloading, 0, backupDir
	s.mu.Unlock()
	newPath := filepath.Join(dir, "zcard.new")
	candidates := s.downloadCandidates(cfg, cli)
	var dlErr error
	for i, cand := range candidates {
		if i > 0 {
			s.mu.Lock()
			s.st.Progress = 0
			s.mu.Unlock()
		}
		if err := downloadTo(ctx, cand, m, assetName, newPath, func(received, total int64) {
			if total > 0 {
				s.mu.Lock()
				s.st.Progress = int32(received * 100 / total)
				s.mu.Unlock()
			}
		}); err != nil {
			_ = os.Remove(newPath)
			dlErr = fmt.Errorf("下载失败（源 %s）: %w", cand.Desc(), err)
			continue
		}
		dlErr = nil
		s.mu.Lock()
		s.st.Source = cand.Desc()
		s.mu.Unlock()
		break
	}
	if dlErr != nil {
		fail(dlErr)
		return
	}

	// 原子替换（rename 舞步 + pending 态）
	s.setPhase(PhaseApplying)
	if err := updater.ApplyFile(s.binPath, cur(), m.Version, newPath); err != nil {
		_ = os.Remove(newPath)
		fail(err)
		return
	}
	s.mu.Lock()
	s.st.Phase, s.st.Target, s.st.Source = PhaseRestarting, m.Version, outcome.SourceDesc()
	s.mu.Unlock()

	// 重启 hook（serve 层三分支；本 goroutine 就此让位——进程即将被停机/替换）
	if s.restartFn != nil {
		s.restartFn()
	}
}

// Rollback 回滚 .prev 并重启（新版不健康时的面板逃生口）。
func (s *Service) Rollback(ctx context.Context) error {
	if s.binPath == "" {
		return errors.New("update: 无法定位当前二进制")
	}
	// 单飞守卫：更新链进行中（下载/落位中）同时回滚 = 磁盘状态竞态破坏
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.mu.Unlock()
	if err := updater.Rollback(s.binPath); err != nil {
		return err
	}
	s.setPhase(PhaseRestarting)
	if s.restartFn != nil {
		go s.restartFn()
	}
	return nil
}

// Snapshot 状态快照（Current/Supervisor/Mode 现算；磁盘 state 合成 verifying/rolled_back）。
func (s *Service) Snapshot(ctx context.Context) Status {
	s.mu.Lock()
	st := s.st
	busy := s.busy
	s.mu.Unlock()
	st.Current = cur()
	st.Busy = busy
	st.Supervisor = s.supervisorKind(ctx)
	st.BackupReady, st.BackupHint = s.backupToolStatus()
	if s.binPath != "" {
		if state, err := updater.LoadState(s.binPath); err == nil && state != nil {
			// 新进程延续目标版本（exec 重启后内存态清零，磁盘 state 是唯一延续源——
			// 前端「等待恢复」以 current==target 判成功，target 断档会假性超时）
			if st.Target == "" && state.ToVer != "" {
				st.Target = state.ToVer
			}
			if st.Phase == PhaseIdle {
				if state.Status == updater.StatePending {
					st.Phase = PhaseVerifying
				} else if state.RolledBack {
					st.Phase = PhaseRolledBack
				}
			}
		}
	}
	if st.Mode == "" {
		st.Mode = s.sourceConfig(ctx).Mode
	}
	return st
}

func (s *Service) setPhase(p string) {
	s.mu.Lock()
	s.st.Phase = p
	if p == PhaseIdle {
		s.st.Err = ""
	}
	s.mu.Unlock()
}

func (s *Service) fail(err error) {
	s.mu.Lock()
	s.st.Phase, s.st.Err = PhaseFailed, err.Error()
	s.mu.Unlock()
}

// backupToolStatus 备份工具就绪检测（方言感知）：更新前强制 DB 备份依赖
// pg_dump/mysqldump——缺失时提前在面板警示安装指引，而非等更新失败才暴露。
func (s *Service) backupToolStatus() (bool, string) {
	if s.dataCfg == nil || s.dataCfg.Database == nil {
		return true, ""
	}
	switch s.dataCfg.Database.Driver {
	case "postgres":
		if _, err := exec.LookPath("pg_dump"); err != nil {
			return false, "当前数据库为 PostgreSQL，服务器缺少 pg_dump——在线更新将因无法备份数据库而中止。请在服务器执行：apt install -y postgresql-client"
		}
	case "mysql":
		if _, err := exec.LookPath("mysqldump"); err != nil {
			return false, "当前数据库为 MySQL，服务器缺少 mysqldump——在线更新将因无法备份数据库而中止。请在服务器执行：apt install -y default-mysql-client"
		}
	}
	return true, ""
}

// cur 当前版本（settings 注入的内存值——重启后自然为新版本号）。
func cur() string { return settings.ServerVersion() }

// SupervisorKind 进程管理器判定（配置显式覆盖 > 自动探测——宝塔等封装环境
// env 探测有盲区，方案 §5）。main 的重启三分支与 status 下发共用本口径，
// 保证「显示的」与「实际分流的」一致。
func (s *Service) SupervisorKind(ctx context.Context) string { return s.supervisorKind(ctx) }

// supervisorKind 配置显式覆盖 > 自动探测（宝塔等封装环境 env 探测有盲区，
// 方案 §5——重启三分支正确分流是更新安全前提，配置是权威出口）。
func (s *Service) supervisorKind(ctx context.Context) string {
	cfg := s.sourceConfig(ctx)
	switch cfg.Supervisor {
	case "systemd", "supervisord", "pm2", "none":
		return cfg.Supervisor
	}
	return updater.DetectSupervisor()
}

// downloadCandidates 下载候选源序列：首选源 + 其余加速器兜底（beta 排除——
// 加速镜像不支持 beta；static/github 钉死模式同样给加速器兜底，网络突变时多一条生路）。
func (s *Service) downloadCandidates(cfg updater.SourceConfig, primary *updater.Client) []*updater.Client {
	out := []*updater.Client{primary}
	if cfg.Channel == "beta" {
		return out
	}
	for _, acc := range cfg.Accels {
		acc = strings.TrimRight(acc, "/")
		if acc == "" || acc == primary.Accel {
			continue
		}
		out = append(out, updater.NewClient(updater.SourceAccel, cfg.Repo, acc, ""))
	}
	if len(out) > 3 { // 最多试 3 个源（重试成本可控）
		out = out[:3]
	}
	return out
}

// downloadTo 流式落盘（DownloadAsset 边写边哈希校验；失败文件由调用方删）。
func downloadTo(ctx context.Context, cli *updater.Client, m *updater.Manifest, name, path string, onProgress updater.ProgressFunc) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := cli.DownloadAsset(ctx, m, name, f, onProgress); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
