// updater 契约测试（P2-07 T3）：更新安全模型 = golden vector 纪律——
// 篡改/换钥/降级/哈希不符全部 fail-closed；rename 舞步原子性与回滚三路径。
package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testManifest(t *testing.T, version string, files []FileEntry) (*Manifest, []byte, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := SignManifest(priv, version, "stable", "test notes", files)
	if err != nil {
		t.Fatal(err)
	}
	m, err := VerifyManifest(raw, pub)
	if err != nil && version != "dev" { // dev 版本 Verify 应拒绝，签名本身合法
		t.Fatalf("roundtrip verify: %v", err)
	}
	if m == nil {
		m = &Manifest{Version: version, Channel: "stable", Files: files}
	}
	return m, raw, pub, priv
}

// ── 验签矩阵（T3-1）──────────────────────────────────────────────

func TestVerifyManifestMatrix(t *testing.T) {
	files := []FileEntry{{Name: "zcard-linux-amd64", SHA256: "ab", Size: 10}}
	m, raw, pub, _ := testManifest(t, "v1.2.3", files)

	if m.Version != "v1.2.3" || len(m.Files) != 1 {
		t.Fatalf("manifest 解析异常: %+v", m)
	}

	// 篡改版本（签名覆盖 content 段——改任何字段都应失效）
	tampered := *m
	tampered.Version = "v9.9.9"
	rawTampered, _ := json.Marshal(tampered)
	if _, err := VerifyManifest(rawTampered, pub); err == nil {
		t.Fatal("篡改 version 后验签必须失败")
	}

	// 篡改文件哈希
	tamperedFiles := *m
	tamperedFiles.Files = []FileEntry{{Name: "evil", SHA256: "00", Size: 1}}
	rawFiles, _ := json.Marshal(tamperedFiles)
	if _, err := VerifyManifest(rawFiles, pub); err == nil {
		t.Fatal("篡改 files 后验签必须失败")
	}

	// 第三方密钥自签（换钥攻击）
	otherPub, _, _ := GenerateKeyPair()
	if _, err := VerifyManifest(raw, otherPub); err == nil {
		t.Fatal("第三方密钥自签必须失败")
	}

	// 签名段损坏
	var badSig Manifest
	_ = json.Unmarshal(raw, &badSig)
	badSig.Signature = base64.StdEncoding.EncodeToString([]byte("garbage"))
	rawBad, _ := json.Marshal(badSig)
	if _, err := VerifyManifest(rawBad, pub); err == nil {
		t.Fatal("损坏签名必须失败")
	}

	// 非 semver 版本（dev 构建）拒绝
	_, rawDev, devPub, _ := testManifest(t, "dev", files)
	if _, err := VerifyManifest(rawDev, devPub); err == nil {
		t.Fatal("非 semver 版本必须拒绝")
	}
}

func TestSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.4", -1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v2.0.0", 0},
		{"dev", "v0.0.1", -1}, // 非 semver 视为最小
		{"1.2.3", "1.2.3", 0}, // 无 v 前缀兼容
	}
	for _, c := range cases {
		if got := CompareSemver(c.a, c.b); got != c.want {
			t.Errorf("CompareSemver(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	if IsSemver("v1.2") || IsSemver("v1.2.3-rc1") || !IsSemver("v1.2.3") {
		t.Fatal("semver 判定异常（预发布 tag 不属 stable 通道）")
	}
}

func TestPublicKeyParse(t *testing.T) {
	pub, _, _ := GenerateKeyPair()
	if _, err := PublicKey(hex.EncodeToString(pub)); err != nil {
		t.Fatalf("合法 hex 公钥解析失败: %v", err)
	}
	if _, err := PublicKey("zz"); err != ErrNoPubkey {
		t.Fatalf("非法公钥应返回 ErrNoPubkey，得到 %v", err)
	}
	if _, err := PublicKey(""); err != ErrNoPubkey {
		t.Fatal("空公钥应返回 ErrNoPubkey")
	}
}

// ── FetchManifest + DownloadAsset（httptest 假源）────────────────

func TestFetchAndDownload(t *testing.T) {
	payload := []byte("fake binary bytes v2")
	sum := sha256.Sum256(payload)
	files := []FileEntry{{Name: "zcard-linux-amd64", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))}}
	m, raw, pub, _ := testManifest(t, "v2.0.0", files)

	asset := payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			base := "http://" + r.Host
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v2.0.0",
				Assets:  []ghAsset{{Name: "update.json", BrowserDownloadURL: base + "/dl/update.json"}},
			})
		case "/dl/update.json":
			_, _ = w.Write(raw)
		case "/releases/download/v2.0.0/zcard-linux-amd64":
			_, _ = w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{API: srv.URL}
	got, _, err := c.FetchManifest(context.Background(), "stable", pub)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if got.Version != m.Version {
		t.Fatalf("版本不符: %s", got.Version)
	}

	// 正常下载（流式哈希校验通过）
	var buf bytes.Buffer
	if err := c.DownloadAsset(context.Background(), got, "zcard-linux-amd64", &buf); err != nil {
		t.Fatalf("DownloadAsset: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatal("下载内容不符")
	}

	// 篡改资产 → ErrFileMismatch（清单哈希与实际不符，fail-closed）
	asset = []byte("tampered payload")
	if err := c.DownloadAsset(context.Background(), got, "zcard-linux-amd64", &bytes.Buffer{}); err != ErrFileMismatch {
		t.Fatalf("哈希不符必须返回 ErrFileMismatch，得到 %v", err)
	}

	// 清单缺产物名
	if err := c.DownloadAsset(context.Background(), got, "zcard-linux-arm64", &bytes.Buffer{}); err != ErrNoAsset {
		t.Fatalf("缺产物必须返回 ErrNoAsset，得到 %v", err)
	}
}

// ── Apply/回滚三路径（T3-2/3）───────────────────────────────────

func setupBin(t *testing.T) (dir, bin string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "zcard")
	if err := os.WriteFile(bin, []byte("v1-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, bin
}

func TestApplyAndRollback(t *testing.T) {
	_, bin := setupBin(t)

	if err := Apply(bin, "v1.0.0", "v1.1.0", []byte("v2-binary")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readFile(t, bin); got != "v2-binary" {
		t.Fatalf("替换后 current 应为新版本: %q", got)
	}
	if got := readFile(t, filepath.Join(filepath.Dir(bin), prevName)); got != "v1-binary" {
		t.Fatalf("回滚位应保留旧版本: %q", got)
	}
	s, err := LoadState(bin)
	if err != nil || s.Status != StatePending || s.FromVer != "v1.0.0" {
		t.Fatalf("pending 态异常: %+v %v", s, err)
	}

	// MarkOK 后：自动回滚路径关闭（随机崩溃不误伤），手动回滚仍可用
	if err := MarkOK(bin, 3); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadState(bin); s.Status != StateOK {
		t.Fatal("MarkOK 后状态应为 ok")
	}
	if RollbackOnBootFailure(bin) {
		t.Fatal("ok 态下启动失败不应触发自动回滚")
	}
	if err := Rollback(bin); err != nil {
		t.Fatalf("手动回滚: %v", err)
	}
	if got := readFile(t, bin); got != "v1-binary" {
		t.Fatalf("回滚后应恢复旧版本: %q", got)
	}
}

func TestRollbackOnBootFailure(t *testing.T) {
	_, bin := setupBin(t)
	if err := Apply(bin, "v1.0.0", "v1.1.0", []byte("bad-new")); err != nil {
		t.Fatal(err)
	}
	if !RollbackOnBootFailure(bin) {
		t.Fatal("pending 态启动失败应执行回滚")
	}
	if got := readFile(t, bin); got != "v1-binary" {
		t.Fatalf("自动回滚后应恢复旧版本: %q", got)
	}
	s, _ := LoadState(bin)
	if s.Status != StateOK || !s.RolledBack {
		t.Fatalf("回滚后状态异常: %+v", s)
	}
	// .prev 已消费，再次回滚报错
	if err := Rollback(bin); err != ErrNoRollback {
		t.Fatalf("无回滚位应报 ErrNoRollback，得到 %v", err)
	}
}

func TestRollbackNoPrev(t *testing.T) {
	_, bin := setupBin(t)
	// 无 pending 无 prev：OnFailure 单元随机触发 → no-op 报错不误伤
	if err := Rollback(bin); err != ErrNoRollback {
		t.Fatalf("应报 ErrNoRollback，得到 %v", err)
	}
}

// ── 健康门（T3-3 成功/不通过双路径）─────────────────────────────

func TestHealthGate(t *testing.T) {
	_, bin := setupBin(t)
	if err := Apply(bin, "v1.0.0", "v1.2.0", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	dbOK := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]bool{"server": true, "database": dbOK},
		})
	}))
	defer srv.Close()

	// 路径一：database 长期 false → 超时报错（不 MarkOK）
	if err := HealthGate(context.Background(), srv.URL, bin, 2*time.Second); err == nil {
		t.Fatal("database 未就绪时健康门必须超时失败")
	}
	s, _ := LoadState(bin)
	if s.Status != StatePending {
		t.Fatal("健康门未通过不得 MarkOK")
	}

	// 路径二：就绪后通过 → MarkOK
	go func() {
		time.Sleep(1 * time.Second)
		dbOK = true
	}()
	if err := HealthGate(context.Background(), srv.URL, bin, 10*time.Second); err != nil {
		t.Fatalf("健康门应通过: %v", err)
	}
	if s, _ = LoadState(bin); s.Status != StateOK {
		t.Fatal("健康门通过后应 MarkOK")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
