// updater 源解析（方案 §4.3）：auto 模式 = 网络环境自动判定。
//
// 语义「中国大陆主机自动走加速镜像，其余直连」，实现不做 geo IP 查询
// （geo 服务墙内不可靠且系额外第三方依赖），而是直接探测 github.com 直连
// 可达性——判定目标本质即「GitHub 通不通」，地理只是代理变量：
//
//	通（≤timeout）→ github 直连（海外主机即此路径）
//	超时/失败      → 并发竞速内置加速器，取最快响应者（大陆主机即此路径）
//	全部不可达     → ErrSourceUnreachable（UI 列出各源状态）
package updater

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SourceConfig 更新源配置（settings system/update 下发；缺省归一）。
type SourceConfig struct {
	Mode       string   `json:"mode"`         // auto | github | accel | static（默认 auto）
	Repo       string   `json:"repo"`         // owner/repo（github/accel；空=DefaultRepo）
	Accels     []string `json:"accelerators"` // 加速前缀列表（空=DefaultAccelerators）
	StaticBase string   `json:"static_base"`  // static 基址（static 模式必填）
	Channel    string   `json:"channel"`      // stable | beta（默认 stable）
	// Supervisor 进程管理器显式覆盖（auto=探测[默认] | systemd | supervisord | pm2 | none）。
	// 探测尽力而为的盲区出口——重启三分支的正确分流比探测更重要（方案 §5）。
	Supervisor string `json:"supervisor,omitempty"`
}

// Normalize 缺省填充（repo/accels/channel；mode 归一非法值到 auto）。
func (c SourceConfig) Normalize() SourceConfig {
	out := c
	switch out.Mode {
	case SourceGitHub, SourceAccel, SourceStatic:
	default:
		out.Mode = "auto"
	}
	if strings.TrimSpace(out.Repo) == "" {
		out.Repo = DefaultRepo
	}
	if len(out.Accels) == 0 {
		out.Accels = DefaultAccelerators
	}
	if out.Channel != "beta" {
		out.Channel = "stable"
	}
	return out
}

// ProbeOutcome 源探测结果（status 接口下发——UI 展示当前源与延迟）。
type ProbeOutcome struct {
	Mode      string        `json:"mode"`       // 生效模式：github | accel | static
	Accel     string        `json:"accel"`      // accel 模式的前缀
	Source    string        `json:"source"`     // 展示串：github | <accel> | static:<base>
	Latency   time.Duration `json:"-"`          // 胜出源探测延迟
	DirectOK  bool          `json:"direct_ok"`  // 直连探测结果（auto 判据）
	DirectMS  int64         `json:"direct_ms"`  // 直连延迟（未通为 0）
	CheckedAt time.Time     `json:"checked_at"` // 探测时刻
}

// SourceDesc 源展示串（status/UI 用）。
func (o *ProbeOutcome) SourceDesc() string {
	if o == nil {
		return ""
	}
	return o.Source
}

// ResolveSource 解析生效源：钉死模式（github/accel/static）直接构造；
// auto 先探测直连，不通则竞速加速器。探测目标即 manifest 端点（HEAD，
// 5s 量级超时由调用方注入——service 层缓存 10min）。
func ResolveSource(ctx context.Context, cfg SourceConfig, timeout time.Duration) (*Client, *ProbeOutcome, error) {
	cfg = cfg.Normalize()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	mk := func(source, accel string) *Client {
		c := NewClient(source, cfg.Repo, accel, cfg.StaticBase)
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
		return c
	}
	switch cfg.Mode {
	case SourceGitHub:
		return mk(SourceGitHub, ""), &ProbeOutcome{Mode: SourceGitHub, Source: "github", CheckedAt: time.Now()}, nil
	case SourceStatic:
		if strings.TrimSpace(cfg.StaticBase) == "" {
			return nil, nil, fmt.Errorf("updater: static 模式未配置 static_base")
		}
		return mk(SourceStatic, ""), &ProbeOutcome{Mode: SourceStatic, Source: "static:" + cfg.StaticBase, CheckedAt: time.Now()}, nil
	case SourceAccel:
		accel := pickFirstAccel(ctx, cfg, timeout)
		if accel == "" {
			return nil, nil, fmt.Errorf("%w（加速器列表 %v）", ErrSourceUnreachable, cfg.Accels)
		}
		return mk(SourceAccel, accel), &ProbeOutcome{Mode: SourceAccel, Accel: accel, Source: accel, CheckedAt: time.Now()}, nil
	}

	// auto：直连探测（ghbase 固定 github.com——探测真实网络环境，非测试注入面）
	directStart := time.Now()
	directOK := probeURL(ctx, "https://github.com/"+cfg.Repo+"/releases/latest/download/update.json", timeout)
	out := &ProbeOutcome{CheckedAt: time.Now(), DirectOK: directOK}
	if directOK {
		out.DirectMS = time.Since(directStart).Milliseconds()
		out.Mode, out.Source, out.Latency = SourceGitHub, "github", time.Since(directStart)
		return mk(SourceGitHub, ""), out, nil
	}
	accel := pickFirstAccel(ctx, cfg, timeout)
	if accel == "" {
		return nil, nil, fmt.Errorf("%w（github 直连与加速器 %v 均不可达）", ErrSourceUnreachable, cfg.Accels)
	}
	out.Mode, out.Accel, out.Source, out.Latency = SourceAccel, accel, accel, 0
	return mk(SourceAccel, accel), out, nil
}

// pickFirstAccel 并发竞速加速器列表，取最快成功者（ghproxy 系死亡是常态）。
func pickFirstAccel(ctx context.Context, cfg SourceConfig, timeout time.Duration) string {
	type hit struct {
		accel   string
		latency time.Duration
	}
	done := make(chan hit, len(cfg.Accels))
	var wg sync.WaitGroup
	for _, accel := range cfg.Accels {
		accel := accel
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			if probeURL(ctx, strings.TrimRight(accel, "/")+"/https://github.com/"+cfg.Repo+"/releases/latest/download/update.json", timeout) {
				done <- hit{accel, time.Since(start)}
			}
		}()
	}
	go func() { wg.Wait(); close(done) }()
	var best *hit
	for h := range done {
		if best == nil || h.latency < best.latency {
			hh := h
			best = &hh
		}
	}
	if best == nil {
		return ""
	}
	return best.accel
}

// probeURL HEAD 探测：**收到任何 HTTP 响应即链路可达**（含 404/405/403——恰恰
// 证明服务端活着；404 是 repo 未发 release 的正常回包，若按状态码判可达会把
// 「首版未发布」误判成「网络不通」）。仅网络层失败（超时/DNS/拒连）为不可达；
// 选错路无实害——后续 manifest 验签 fail-closed 兜底。
func probeURL(ctx context.Context, url string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}
