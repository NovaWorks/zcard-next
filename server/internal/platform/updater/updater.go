// Package updater 在线更新引擎（P2-07，主文档 §10.4——「重写 1.x 最危险组件」）：
//
//	ed25519 强制验签的 release manifest + 原子替换（rename 舞步）+ 状态机
//	（pending→ok）+ 自动回滚。重启交给 systemd——进程只负责「原子替换文件 +
//	干净退出」，绝不 fork/exec 拉起自己。
//
// 密码学原语与 platform/license 同源（ed25519 单文件内嵌签名），但使用
//
//	独立密钥对（license 一把、更新一把，互不连坐）。
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
	ErrBadManifest  = errors.New("updater: manifest 非法或验签失败")
	ErrFileMismatch = errors.New("updater: 产物哈希/大小与清单不符")
	ErrNoAsset      = errors.New("updater: release 缺少所需产物")
	ErrRefuseUpdate = errors.New("updater: 拒绝更新（版本非单调或非 semver）")
	ErrNoPubkey     = errors.New("updater: 未配置更新公钥（dev 构建）")
)

// DefaultPublicKeyHex 编译期注入（-X；首发密钥固化进仓库默认值）。空 = dev 构建，
// self-update 一律拒绝（fail-closed），仅 sign/genkey 可用。
var DefaultPublicKeyHex string

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

// GitHub Releases API 载荷（只取所需字段）。
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

// Client 更新检查器（API 源可注入——测试走 httptest）。
type Client struct {
	API   string       // GitHub Releases API 根（如 https://api.github.com/repos/<owner>/<repo>）
	HTTP  *http.Client // 默认 30s 超时
	Token string       // 可选 GitHub Token（私有仓库/限流缓解）
}

// FetchManifest 拉取并验签最新 release 的 update.json。
// channel=stable 取 /releases/latest（GitHub 语义本身排除 prerelease）；
// beta 取 /releases 列表首个 prerelease。
func (c *Client) FetchManifest(ctx context.Context, channel string, pub ed25519.PublicKey) (*Manifest, string, error) {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	url := strings.TrimRight(c.API, "/") + "/releases/latest"
	if channel == "beta" {
		url = strings.TrimRight(c.API, "/") + "/releases?per_page=20"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("updater: 检查更新失败（%s HTTP %d）", url, resp.StatusCode)
	}
	pick := func(rel ghRelease) (string, error) {
		for _, a := range rel.Assets {
			if a.Name == "update.json" {
				return a.BrowserDownloadURL, nil
			}
		}
		return "", ErrNoAsset
	}
	var manifestURL string
	if channel == "beta" {
		var rels []ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
			return nil, "", err
		}
		found := false
		for _, rel := range rels {
			if rel.Prerelease {
				if manifestURL, err = pick(rel); err == nil {
					found = true
				}
				break
			}
		}
		if !found {
			return nil, "", ErrNoAsset
		}
	} else {
		var rel ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return nil, "", err
		}
		if manifestURL, err = pick(rel); err != nil {
			return nil, "", err
		}
	}
	raw, err := c.download(ctx, manifestURL)
	if err != nil {
		return nil, "", err
	}
	m, err := VerifyManifest(raw, pub)
	if err != nil {
		return nil, "", err
	}
	return m, manifestURL, nil
}

// DownloadAsset 下载指定产物到 w（流式哈希校验，不符即 ErrFileMismatch 且不落完整盘）。
func (c *Client) DownloadAsset(ctx context.Context, m *Manifest, name string, w io.Writer) error {
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
	// 资产 URL 从 tag 现算（清单本身已验签，tag 即清单 version）
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	url := strings.TrimRight(c.API, "/") + "/releases/download/" + m.Version + "/" + name
	resp, err := c.HTTP.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("updater: 下载失败（%s HTTP %d）", url, resp.StatusCode)
	}
	sum, size, err := HashFile(io.TeeReader(resp.Body, w))
	if err != nil {
		return err
	}
	f := m.Files[entry]
	if sum != strings.ToLower(f.SHA256) || size != f.Size {
		return ErrFileMismatch
	}
	return nil
}

func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
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
