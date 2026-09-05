// updater 契约测试（P2-07 T3；2026-09 三源重构后增补）：更新安全模型 =
// golden vector 纪律——篡改/换钥/降级/哈希不符全部 fail-closed；
// rename 舞步原子性（内存/落盘双入口）与回滚三路径；三源 URL 构造与 auto 探测。
package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	// 固化公钥可用（首发密钥健康自检）
	if _, err := PublicKey(DefaultPublicKeyHex); err != nil {
		t.Fatalf("仓库固化公钥非法: %v", err)
	}
}

// ── 三源 Fetch/Download（httptest 假源）────────────────────────

// fakeSource 起一个同时模拟 github 直连 / accel 加速 / static 三种形态的假源。
// ghBase 充当 github.com；accelSrv 充当加速器（透传到 ghBase 的完整 URL 路径）。
func fakeSource(t *testing.T, payload []byte, raw []byte) (ghBase, staticBase, accelBase string, closeFn func()) {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/update.json"):
			_, _ = w.Write(raw)
		case strings.HasSuffix(r.URL.Path, "/"+assetName()):
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	accel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ghproxy 形态：/<完整 github URL> —— 剥协议头还原路径转发到假 github
		idx := strings.Index(r.URL.Path, "/https://github.com/")
		if idx < 0 {
			http.NotFound(w, r)
			return
		}
		resp, err := http.Get(gh.URL + r.URL.Path[idx+len("/https://github.com"):])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = copyTo(w, resp.Body)
	}))
	return gh.URL, gh.URL, accel.URL, func() { gh.Close(); accel.Close() }
}

func assetName() string { return "zcard-linux-amd64" }

func copyTo(w http.ResponseWriter, r interface{ Read([]byte) (int, error) }) (int64, error) {
	buf := make([]byte, 32*1024)
	var n int64
	for {
		k, err := r.Read(buf)
		if k > 0 {
			if _, werr := w.Write(buf[:k]); werr != nil {
				return n, werr
			}
			n += int64(k)
		}
		if err != nil {
			return n, nil // io.EOF
		}
	}
}

func TestFetchAndDownloadThreeSources(t *testing.T) {
	payload := []byte("fake binary bytes v2")
	sum := sha256.Sum256(payload)
	files := []FileEntry{{Name: assetName(), SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))}}
	m, raw, pub, _ := testManifest(t, "v2.0.0", files)

	gh, static, accel, closeFn := fakeSource(t, payload, raw)
	defer closeFn()

	repo := "acme/zcard"
	for _, tc := range []struct {
		name   string
		client *Client
	}{
		{"github 直连（latest/download 端点）", func() *Client {
			c := NewClient(SourceGitHub, repo, "", "")
			c.GHBase = gh
			return c
		}()},
		{"accel 加速（前缀拼接完整 URL）", NewClient(SourceAccel, repo, accel, "")},
		{"static 静态源", NewClient(SourceStatic, repo, "", static)},
	} {
		got, _, err := tc.client.FetchManifest(context.Background(), "stable", pub)
		if err != nil {
			t.Fatalf("[%s] FetchManifest: %v", tc.name, err)
		}
		if got.Version != m.Version {
			t.Fatalf("[%s] 版本不符: %s", tc.name, got.Version)
		}
		out := &strings.Builder{}
		var lastReceived int64
		if err := tc.client.DownloadAsset(context.Background(), got, assetName(), out, func(received, total int64) {
			lastReceived = received
		}); err != nil {
			t.Fatalf("[%s] DownloadAsset: %v", tc.name, err)
		}
		if out.String() != string(payload) {
			t.Fatalf("[%s] 下载内容不符", tc.name)
		}
		if lastReceived != int64(len(payload)) {
			t.Fatalf("[%s] 进度回调未收到末值: %d", tc.name, lastReceived)
		}

		// 篡改资产 → ErrFileMismatch（fail-closed；清单声明尺寸与实际不符）
		bad := append([]byte(nil), payload...)
		bad[0] ^= 0xff
		badSum := sha256.Sum256(bad)
		badFiles := []FileEntry{{Name: assetName(), SHA256: hex.EncodeToString(badSum[:]), Size: int64(len(bad)) + 1}}
		badM, badRaw, _, _ := testManifest(t, "v2.0.1", badFiles)
		_, st2, _, close2 := fakeSource(t, bad, badRaw)
		defer close2()
		c2 := NewClient(SourceStatic, repo, "", st2)
		if err := c2.DownloadAsset(context.Background(), badM, assetName(), &strings.Builder{}, nil); err != ErrFileMismatch {
			t.Fatalf("[%s] 哈希不符必须返回 ErrFileMismatch，得到 %v", tc.name, err)
		}

		// 清单缺产物名
		if err := tc.client.DownloadAsset(context.Background(), got, "zcard-linux-other", &strings.Builder{}, nil); err != ErrNoAsset {
			t.Fatalf("[%s] 缺产物必须返回 ErrNoAsset，得到 %v", tc.name, err)
		}
	}
}

func TestAccelBetaRejected(t *testing.T) {
	c := NewClient(SourceAccel, "acme/zcard", "https://gh-proxy.com", "")
	if _, err := c.manifestURL("beta"); err != ErrBetaSource {
		t.Fatalf("accel beta 必须拒绝，得到 %v", err)
	}
}

// ── auto 源探测（直连可达→github；不通→竞速加速器）──────────────

func TestResolveSourceAuto(t *testing.T) {
	payload := []byte("x")
	sum := sha256.Sum256(payload)
	files := []FileEntry{{Name: assetName(), SHA256: hex.EncodeToString(sum[:]), Size: 1}}
	_, raw, pub, _ := testManifest(t, "v3.0.0", files)

	// 场景一：直连可达（github.com 指向假源不可行——auto 探测 URL 硬编码真实
	// github 域名；此处验证钉死模式 + 竞速逻辑的单元面）
	gh, static, accel, closeFn := fakeSource(t, payload, raw)
	defer closeFn()
	_ = pub

	// 钉死 github
	c, out, err := ResolveSource(context.Background(), SourceConfig{Mode: SourceGitHub, Repo: "a/b"}, time.Second)
	if err != nil || c.Source != SourceGitHub || out.Mode != SourceGitHub {
		t.Fatalf("钉死 github 失败: %v %+v", err, out)
	}
	// 钉死 static
	if _, _, err := ResolveSource(context.Background(), SourceConfig{Mode: SourceStatic, StaticBase: static}, time.Second); err != nil {
		t.Fatalf("钉死 static 失败: %v", err)
	}
	// 钉死 static 缺 base
	if _, _, err := ResolveSource(context.Background(), SourceConfig{Mode: SourceStatic}, time.Second); err == nil {
		t.Fatal("static 缺 base 必须报错")
	}
	// 钉死 accel：加速器全不可达 → ErrSourceUnreachable
	if _, _, err := ResolveSource(context.Background(), SourceConfig{Mode: SourceAccel, Accels: []string{"http://127.0.0.1:1"}}, 500*time.Millisecond); err == nil {
		t.Fatal("accel 全挂必须报 ErrSourceUnreachable")
	}
	// 钉死 accel：可用加速器胜出
	c, out, err = ResolveSource(context.Background(), SourceConfig{Mode: SourceAccel, Accels: []string{accel}}, 3*time.Second)
	if err != nil || out.Accel != accel || c.Accel != accel {
		t.Fatalf("accel 竞速胜出失败: %v %+v", err, out)
	}
	_ = gh

	// auto：真实 github.com 直连在本测试环境不可达时走加速竞速——网络相关的
	// 行为断言放集成环境，此处仅验证 auto 归一（Mode 非法值回 auto）。
	if got := (SourceConfig{Mode: "weird"}).Normalize().Mode; got != "auto" {
		t.Fatalf("非法 mode 须归一 auto，得到 %s", got)
	}
}

// ── Apply/回滚三路径（内存版 + 落盘版双入口）───────────────────

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

// TestApplyFile 落盘入口（生产更新链路径）：预下载 .new → 舞步落位。
func TestApplyFile(t *testing.T) {
	dir, bin := setupBin(t)
	newPath := filepath.Join(dir, newName)
	if err := os.WriteFile(newPath, []byte("v2-disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFile(bin, "v1.0.0", "v2.0.0", newPath); err != nil {
		t.Fatalf("ApplyFile: %v", err)
	}
	if got := readFile(t, bin); got != "v2-disk" {
		t.Fatalf("落盘版替换失败: %q", got)
	}
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatal("落位后必须保有执行位")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal(".new 落位后不应残留")
	}
	s, _ := LoadState(bin)
	if s.Status != StatePending || s.ToVer != "v2.0.0" {
		t.Fatalf("落盘版 pending 态异常: %+v", s)
	}

	// 跨目录拒绝（rename 原子性前提）
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFile(bin, "v1.0.0", "v2.0.0", other); err == nil {
		t.Fatal("跨目录 ApplyFile 必须拒绝")
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

// ── 健康门（成功/不通过双路径）────────────────────────────────

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

// ── 磁盘预检 / supervisor 探测 ────────────────────────────────

func TestCheckDiskSpace(t *testing.T) {
	dir := t.TempDir()
	if err := CheckDiskSpace(dir, 1); err != nil {
		t.Fatalf("1 字节预检不应失败: %v", err)
	}
	if err := CheckDiskSpace(dir, 1<<62); err == nil {
		t.Fatal("超大需求必须报空间不足")
	}
}

func TestDetectSupervisor(t *testing.T) {
	t.Setenv("ZCARD_SUPERVISOR", "")
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("SUPERVISOR_ENABLED", "")
	t.Setenv("SUPERVISOR_SERVER_URL", "")
	if got := DetectSupervisor(); got != "none" {
		t.Fatalf("无标记环境应为 none，得到 %s", got)
	}
	t.Setenv("ZCARD_SUPERVISOR", "systemd")
	if got := DetectSupervisor(); got != "systemd" {
		t.Fatalf("显式声明优先，得到 %s", got)
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
