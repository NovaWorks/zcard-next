// Package updater 在线更新引擎（P2-07，主文档 §10.4——「重写 1.x 最危险组件」；
// 2026-09 在线更新方案 doc/在线更新方案.md 定稿重构）：
//
//	ed25519 强制验签的 release manifest + 原子替换（rename 舞步）+ 状态机
//	（pending→ok）+ 自动回滚。重启策略三分支见方案 §5——进程管理器
//	（systemd/supervisord）为主，裸跑由 serve 层 syscall.Exec 降级。
//
//	更新源三型（方案 §4）：github 直连 / accel 加速镜像（ghproxy 系前缀拼接）/
//	static 自建静态源。manifest 统一走 github.com 官方 releases/latest/download
//	重定向端点（加速器不代理 REST API；直连亦免 60 次/h 匿名限流）；
//	beta 通道需列 prerelease，仅 github 直连支持。
//
// 密码学原语与 platform/license 同源（ed25519 单文件内嵌签名），但使用
// 独立密钥对（license 一把、更新一把，互不连坐）。
package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// 更新安全模型的 fail-closed 错误（任一命中：磁盘零变更）。
var (
	ErrBadManifest       = errors.New("updater: manifest 非法或验签失败")
	ErrFileMismatch      = errors.New("updater: 产物哈希/大小与清单不符")
	ErrNoAsset           = errors.New("updater: release 缺少所需产物")
	ErrRefuseUpdate      = errors.New("updater: 拒绝更新（版本非单调或非 semver）")
	ErrNoPubkey          = errors.New("updater: 未配置更新公钥（dev 构建）")
	ErrBetaSource        = errors.New("updater: beta 通道仅支持 github 直连/自建静态源（加速镜像不走 REST API）")
	ErrSourceUnreachable = errors.New("updater: 更新源均不可达")
)

// DefaultPublicKeyHex 首发更新公钥（2026-09-05 genkey 固化；ldflags -X 可覆盖——
// 密钥轮换期过渡用）。私钥离线保管（发行侧 sign 子命令），泄露即换钥发版。
var DefaultPublicKeyHex = "e7d28f99b52cda5e2596c4bea8b125c8c29cb4ca07af83aa5fe2a898c0e587cd"

// DefaultRepo 发行仓库（github/accel 源；编译期 -X 可覆盖）。
var DefaultRepo = "NovaWorks/zcard-next"

// DefaultAccelerators 内置加速镜像前缀（方案 §4.2 实测 2026-09-05 筛出；
// 加速器死亡是常态——列表在 settings 可配，此处仅为默认值）。
var DefaultAccelerators = []string{
	"https://gh-proxy.com", // 实测 206 直出 + Range 断点
	"https://ghfast.top",   // 302 规范化 latest→具体 tag 后代理
	"https://ghproxy.net",  // 同上
}

// 源类型。
const (
	SourceGitHub = "github" // github.com 直连（海外/网络通畅）
	SourceAccel  = "accel"  // ghproxy 系加速镜像（中国大陆）
	SourceStatic = "static" // 自建静态源（商业发行；<base>/update.json + 裸产物）
)

// FileEntry 单个发布产物（双架构二进制）。
type FileEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"` // hex
	Size   int64  `json:"size"`
}

// Manifest 发布清单（验签覆盖 content 段全部字段）。
type Manifest struct {
	Version   string      `json:"version"` // vMAJOR.MINOR.PATCH
	Channel   string      `json:"channel"` // stable | beta
	Notes     string      `json:"notes"`   // changelog markdown
	IssuedAt  string      `json:"issued_at"`
	Files     []FileEntry `json:"files"`
	Signature string      `json:"signature"` // base64(ed25519.Sign(priv, content))
}

// GitHub Releases API 载荷（beta 通道列 prerelease 用；仅 github 直连）。
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// ParseSemver 解析 vMAJOR.MINOR.PATCH（dev/脏构建等非 semver 一律 nil）。
func ParseSemver(v string) [3]int {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{-1, -1, -1}
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		_, _ = fmt.Sscanf(m[i+1], "%d", &out[i])
	}
	return out
}

// IsSemver 是否合法 semver。
func IsSemver(v string) bool { return ParseSemver(v)[0] >= 0 }

// CompareSemver 比较 a、b：-1/0/1。任一非 semver 视为最小。
func CompareSemver(a, b string) int {
	sa, sb := ParseSemver(a), ParseSemver(b)
	for i := 0; i < 3; i++ {
		if sa[i] != sb[i] {
			if sa[i] < sb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// VerifyManifest 验签并解析 manifest（与 license.Verify 同构：重编码 content 段
// 作为签名原文，防字段注入）。
func VerifyManifest(raw []byte, pub ed25519.PublicKey) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, ErrBadManifest
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return nil, ErrBadManifest
	}
	content, err := json.Marshal(Manifest{
		Version: m.Version, Channel: m.Channel, Notes: m.Notes,
		IssuedAt: m.IssuedAt, Files: m.Files,
	})
	if err != nil {
		return nil, ErrBadManifest
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, content, sig) {
		return nil, ErrBadManifest
	}
	if !IsSemver(m.Version) || len(m.Files) == 0 {
		return nil, ErrBadManifest
	}
	return &m, nil
}

// SignManifest 发行侧签发（私钥离线保管，见 self-update sign 子命令）。
func SignManifest(priv ed25519.PrivateKey, version, channel, notes string, files []FileEntry) ([]byte, error) {
	m := Manifest{
		Version: version, Channel: channel, Notes: notes,
		IssuedAt: time.Now().UTC().Format(time.RFC3339), Files: files,
	}
	content, err := json.Marshal(Manifest{
		Version: m.Version, Channel: m.Channel, Notes: m.Notes,
		IssuedAt: m.IssuedAt, Files: m.Files,
	})
	if err != nil {
		return nil, err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, content))
	return json.MarshalIndent(m, "", "  ")
}

// HashFile 计算流式 sha256（下载即校验，不落盘两遍）。
func HashFile(r io.Reader) (hexSum string, size int64, err error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Client 更新客户端（三源统一；源字段见 Source* 常量，测试经 GHBase/APIBase 注入）。
type Client struct {
	Source     string // github | accel | static
	Repo       string // "owner/repo"（github/accel）
	Accel      string // 加速前缀如 https://gh-proxy.com（accel）
	StaticBase string // 静态源基址（static；update.json 与产物平铺其下）
	GHBase     string // github 页面基址（默认 https://github.com；测试注入）
	APIBase    string // github REST 基址（默认 https://api.github.com；仅 beta 通道）
	HTTP       *http.Client
	Token      string // 可选 GitHub Token（beta 通道限流缓解）
}

// NewClient 构造指定源的客户端（生产默认基址；测试可再覆写 GHBase/APIBase）。
func NewClient(source, repo, accel, staticBase string) *Client {
	return &Client{
		Source: source, Repo: repo, Accel: accel, StaticBase: staticBase,
		GHBase: "https://github.com", APIBase: "https://api.github.com",
	}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// githubPath github 页面路径（release 下载端点共用拼接）。
func (c *Client) githubPath(p string) string {
	return strings.TrimRight(c.GHBase, "/") + "/" + strings.Trim(c.Repo, "/") + "/" + p
}

// accelPrefix 加速前缀（ghproxy 系：<prefix>/https://github.com/...）。
func (c *Client) accelPrefix() string {
	return strings.TrimRight(c.Accel, "/") + "/"
}

// Desc 源展示串（status/UI/下载换源重试日志用）。
func (c *Client) Desc() string {
	switch c.Source {
	case SourceStatic:
		return "static:" + c.StaticBase
	case SourceAccel:
		return c.Accel
	default:
		return "github"
	}
}

// manifestURL 按 source/channel 拼manifest 地址。
func (c *Client) manifestURL(channel string) (string, error) {
	const name = "update.json"
	switch c.Source {
	case SourceStatic:
		base := strings.TrimRight(c.StaticBase, "/")
		if base == "" {
			return "", fmt.Errorf("updater: static 源缺 StaticBase")
		}
		if channel == "beta" {
			return base + "/beta/" + name, nil
		}
		return base + "/" + name, nil
	case SourceAccel:
		if channel == "beta" {
			return "", ErrBetaSource
		}
		if c.Accel == "" {
			return "", fmt.Errorf("updater: accel 源缺 Accel 前缀")
		}
		return c.accelPrefix() + c.githubPath("releases/latest/download/"+name), nil
	default: // github 直连
		return c.githubPath("releases/latest/download/" + name), nil
	}
}

// assetURL 产物地址（version 来自已验签 manifest——tag 即版本，无注入面）。
func (c *Client) assetURL(version, name string) string {
	switch c.Source {
	case SourceStatic:
		return strings.TrimRight(c.StaticBase, "/") + "/" + name
	case SourceAccel:
		return c.accelPrefix() + c.githubPath("releases/download/"+version+"/"+name)
	default:
		return c.githubPath("releases/download/" + version + "/" + name)
	}
}

// FetchManifest 拉取并验签最新 release 的 update.json。
// stable：github 官方 releases/latest/download 重定向端点（三源同构；
// 加速镜像实测正确代理该语义，自身完成 latest→具体 tag 的规范化）。
// beta：需列 prerelease——仅 github 直连走 REST API（静态源按 <base>/beta/ 约定）。
func (c *Client) FetchManifest(ctx context.Context, channel string, pub ed25519.PublicKey) (*Manifest, string, error) {
	if channel == "beta" && c.Source == SourceGitHub {
		return c.fetchBetaViaAPI(ctx, pub)
	}
	url, err := c.manifestURL(channel)
	if err != nil {
		return nil, "", err
	}
	raw, err := c.download(ctx, url)
	if err != nil {
		return nil, "", err
	}
	m, err := VerifyManifest(raw, pub)
	if err != nil {
		return nil, "", err
	}
	return m, url, nil
}

// fetchBetaViaAPI beta 通道（github 直连）：/releases 列表取首个 prerelease
// 的 update.json 资产直链（加速镜像不代理 REST API，已在 manifestURL 拒绝）。
func (c *Client) fetchBetaViaAPI(ctx context.Context, pub ed25519.PublicKey) (*Manifest, string, error) {
	url := strings.TrimRight(c.APIBase, "/") + "/repos/" + strings.Trim(c.Repo, "/") + "/releases?per_page=20"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("updater: 检查更新失败（%s HTTP %d）", url, resp.StatusCode)
	}
	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, "", err
	}
	for _, rel := range rels {
		if !rel.Prerelease {
			continue
		}
		for _, a := range rel.Assets {
			if a.Name != "update.json" {
				continue
			}
			raw, err := c.download(ctx, a.BrowserDownloadURL)
			if err != nil {
				return nil, "", err
			}
			m, err := VerifyManifest(raw, pub)
			if err != nil {
				return nil, "", err
			}
			return m, a.BrowserDownloadURL, nil
		}
	}
	return nil, "", ErrNoAsset
}

// ProgressFunc 下载进度回调（received 已收字节；total 为 manifest 声明大小，未知为 0）。
type ProgressFunc func(received, total int64)

// DownloadAsset 下载指定产物到 w（流式哈希校验，不符即 ErrFileMismatch；
// 大产物经 onProgress 报进度——124MB 二进制必须落盘不能进内存，方案 §8）。
func (c *Client) DownloadAsset(ctx context.Context, m *Manifest, name string, w io.Writer, onProgress ProgressFunc) error {
	entry := -1
	for i, f := range m.Files {
		if f.Name == name {
			entry = i
			break
		}
	}
	if entry < 0 {
		return ErrNoAsset
	}
	url := c.assetURL(m.Version, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("updater: 下载失败（%s HTTP %d）", url, resp.StatusCode)
	}
	f := m.Files[entry]
	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{r: resp.Body, total: f.Size, fn: onProgress}
	}
	sum, size, err := HashFile(io.TeeReader(src, w))
	if err != nil {
		return err
	}
	if sum != strings.ToLower(f.SHA256) || size != f.Size {
		return ErrFileMismatch
	}
	return nil
}

// progressReader 读取计数包装（周期性回调，避免高频锁竞争）。
type progressReader struct {
	r     io.Reader
	total int64
	n     int64
	last  time.Time
	fn    ProgressFunc
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if now := time.Now(); p.last.IsZero() || now.Sub(p.last) >= 300*time.Millisecond {
		p.last = now
		p.fn(p.n, p.total)
	}
	return n, err
}

func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: 下载 update.json 失败（HTTP %d）", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// PublicKey 解析 hex 公钥（CLI -pubkey / 编译默认值共用）。
func PublicKey(hexKey string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, ErrNoPubkey
	}
	return ed25519.PublicKey(b), nil
}

// GenerateKeyPair 生成更新密钥对（genkey 子命令；私钥离线保管）。
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(nil)
}
