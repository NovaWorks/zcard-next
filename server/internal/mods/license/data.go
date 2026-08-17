package license

// M3 订阅许可证模块（主文档 §11 第 1 层落地）：
//   settings license 组存储（file=许可证内容 / pubkey=ed25519 公钥 / instance_id=实例 ID）；
//   安装 = 校验（签名/实例绑定/到期）通过后落库；查询 = 实时 Verify fail-closed。
// 社区版无公钥/无许可证 → 全部高级特性关闭（开源核心功能不受影响）。

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/license"
)

// settingsStore 设置读写窄接口（consumer-side，settings.RepoImpl 满足，通道 A）。
type settingsStore interface {
	Get(ctx context.Context, group, key string) (json.RawMessage, error)
	Put(ctx context.Context, group, key string, value json.RawMessage) error
}

// LicenseRepo 许可证仓储。
type LicenseRepo struct {
	settings settingsStore
}

// NewLicenseRepo 构造。
func NewLicenseRepo(settings settingsStore) *LicenseRepo {
	return &LicenseRepo{settings: settings}
}

// InstanceID 读取或生成实例 ID（生成后持久化，许可证绑定用）。
func (r *LicenseRepo) InstanceID(ctx context.Context) (string, error) {
	raw, err := r.settings.Get(ctx, "license", "instance_id")
	if err == nil && len(raw) > 0 && string(raw) != "null" && string(raw) != `""` {
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			return v, nil
		}
	}
	id := randomInstanceID()
	if err := r.settings.Put(ctx, "license", "instance_id", json.RawMessage(`"`+id+`"`)); err != nil {
		return "", err
	}
	return id, nil
}

// Status 许可证状态。
type Status struct {
	Installed  bool
	Valid      bool
	InstanceID string
	Domain     string
	Features   []string
	ExpiresAt  string
	Error      string
}

// Status 查询：读许可证 + 公钥 → Verify（fail-closed）。
func (r *LicenseRepo) Status(ctx context.Context) Status {
	st := Status{InstanceID: "", Installed: false, Valid: false}
	id, err := r.InstanceID(ctx)
	if err != nil {
		st.Error = "实例 ID 生成失败"
		return st
	}
	st.InstanceID = id
	raw, _ := r.settings.Get(ctx, "license", "file")
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		st.Error = "未安装许可证（社区版）"
		return st
	}
	var content string
	if json.Unmarshal(raw, &content) != nil || content == "" {
		st.Error = "许可证存储损坏"
		return st
	}
	st.Installed = true
	pub, err := r.PubKey(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	lic, err := license.Verify([]byte(content), pub, id, r.domain(ctx), time.Now().UTC())
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Valid = true
	st.Domain = lic.Domain
	st.Features = lic.Features
	st.ExpiresAt = lic.ExpiresAt
	return st
}

// Install 校验并安装许可证（绑定当前实例；失败不落库）。
func (r *LicenseRepo) Install(ctx context.Context, content string) error {
	pub, err := r.PubKey(ctx)
	if err != nil {
		return err
	}
	id, err := r.InstanceID(ctx)
	if err != nil {
		return err
	}
	if _, err := license.Verify([]byte(content), pub, id, r.domain(ctx), time.Now().UTC()); err != nil {
		return err
	}
	return r.settings.Put(ctx, "license", "file", json.RawMessage(`"`+jsonEscape(content)+`"`))
}

// Clear 清除许可证。
func (r *LicenseRepo) Clear(ctx context.Context) error {
	return r.settings.Put(ctx, "license", "file", json.RawMessage(`""`))
}

// PubKey 读取 ed25519 公钥（base64；未配置返回 ErrInvalidLicense）。
func (r *LicenseRepo) PubKey(ctx context.Context) (ed25519PublicKey, error) {
	raw, _ := r.settings.Get(ctx, "license", "pubkey")
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil, license.ErrInvalidLicense
	}
	var b64 string
	if json.Unmarshal(raw, &b64) != nil {
		return nil, license.ErrInvalidLicense
	}
	pub, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(pub) != ed25519PublicKeySize {
		return nil, license.ErrInvalidLicense
	}
	return pub, nil
}

// domain 主站域名（license 组 domain 配置；空 = 跳过域名绑定）。
func (r *LicenseRepo) domain(ctx context.Context) string {
	raw, _ := r.settings.Get(ctx, "license", "domain")
	if len(raw) == 0 {
		return ""
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	return v
}

// ── 工具 ──

type ed25519PublicKey = []byte

const ed25519PublicKeySize = 32

func randomInstanceID() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return "zcard-inst"
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 32)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0xf]
	}
	return "zcard-" + string(out)
}

func jsonEscape(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw[1 : len(raw)-1])
}
