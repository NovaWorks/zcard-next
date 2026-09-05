// self-update 子命令（P2-07 T2；2026-09 方案 §4/§8 重构）：在线更新 CLI 面。
//
//	zcard self-update [--check] [--rollback] [-conf <dir>]
//	                  [-source auto|github|accel|static] [-repo <owner/repo>]
//	                  [-accel <prefix[,prefix...]>] [-base <url>] [-channel stable|beta]
//	                  [-pubkey <hex>] [-y]
//	zcard self-update genkey                                # 发行侧密钥对
//	zcard self-update sign --key <file> --dir <dist> --version vX.Y.Z [--notes-file <md>]
//
// 安全模型见 platform/updater 包注释与 doc/在线更新方案.md；
// 非 TTY 且无 -y 拒绝执行（防脚本误触）。
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/update"
	"github.com/NovaWorks/zcard-next/server/internal/platform/updater"
)

func runSelfUpdate(args []string) error {
	// 发行侧工具子形态（与更新执行互斥）
	if len(args) > 0 {
		switch args[0] {
		case "genkey":
			return runGenKey()
		case "sign":
			return runSign(args[1:])
		}
	}

	fs := flag.NewFlagSet("self-update", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	check := fs.Bool("check", false, "只检查更新不执行（输出 JSON）")
	rollback := fs.Bool("rollback", false, "回滚到上一版本（zcard.prev）")
	source := fs.String("source", "auto", "更新源 auto|github|accel|static（auto=直连探测，不通走加速）")
	repo := fs.String("repo", updater.DefaultRepo, "发行仓库 owner/repo")
	accel := fs.String("accel", "", "加速前缀（逗号分隔多个；source=accel/auto 时用，缺省内置列表）")
	base := fs.String("base", "", "静态源基址（source=static 必填）")
	channel := fs.String("channel", "stable", "更新通道 stable|beta（beta 仅直连/静态源）")
	pubkeyHex := fs.String("pubkey", "", "更新公钥 hex（空=编译默认；轮换期过渡用）")
	yes := fs.Bool("y", false, "跳过确认（非 TTY 必须显式指定）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("self-update: 定位当前二进制失败: %w", err)
	}
	binPath, _ = filepath.EvalSymlinks(binPath)

	if *rollback {
		if err := updater.Rollback(binPath); err != nil {
			return fmt.Errorf("self-update: 回滚失败: %w", err)
		}
		fmt.Println("已回滚到上一版本（systemd 将于下次重启生效；手动环境请重启服务）")
		return nil
	}

	pubHex := strings.TrimSpace(*pubkeyHex)
	if pubHex == "" {
		pubHex = updater.DefaultPublicKeyHex
	}
	pub, err := updater.PublicKey(pubHex)
	if err != nil {
		return fmt.Errorf("self-update: %v（dev 构建未注入公钥，或用 -pubkey 指定）", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg := updater.SourceConfig{
		Mode: *source, Repo: *repo, StaticBase: *base, Channel: *channel,
	}
	if v := strings.TrimSpace(*accel); v != "" {
		cfg.Accels = strings.Split(v, ",")
	}
	cli, outcome, err := updater.ResolveSource(ctx, cfg, 5*time.Second)
	if err != nil {
		return fmt.Errorf("self-update: %w", err)
	}
	m, manifestURL, err := cli.FetchManifest(ctx, *channel, pub)
	if err != nil {
		return fmt.Errorf("self-update: %w（源 %s）", err, outcome.SourceDesc())
	}

	if *check {
		out, err := json.MarshalIndent(map[string]any{
			"current": orDev(Version), "latest": m.Version,
			"channel": m.Channel, "notes": m.Notes, "manifest": manifestURL,
			"source":           outcome.SourceDesc(),
			"update_available": updater.CompareSemver(m.Version, Version) > 0,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	if m.Version == Version {
		fmt.Printf("已是最新版本 %s\n", Version)
		return nil
	}
	// 版本单调：拒绝降级与非 semver 目标（防降级攻击/误更到 dev）
	if !updater.IsSemver(m.Version) || updater.CompareSemver(m.Version, Version) <= 0 {
		return fmt.Errorf("self-update: %w（当前 %s ↔ 目标 %s）", updater.ErrRefuseUpdate, orDev(Version), m.Version)
	}

	assetName := "zcard-" + runtime.GOOS + "-" + runtime.GOARCH
	var assetSize int64
	for _, f := range m.Files {
		if f.Name == assetName {
			assetSize = f.Size
		}
	}
	fmt.Printf("发现新版本 %s → %s（源 %s）\n\n变更记录:\n%s\n\n", orDev(Version), m.Version, outcome.SourceDesc(), m.Notes)
	if !*yes {
		if !isTTY() {
			return fmt.Errorf("self-update: 非 TTY 环境需 -y 显式确认")
		}
		fmt.Print("确认更新? [y/N] ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			fmt.Println("已取消")
			return nil
		}
	}

	// 磁盘预检（方案 §8）：产物 + 64MB 余量（DB 备份另计由 dump 工具自行失败）
	if err := updater.CheckDiskSpace(filepath.Dir(binPath), assetSize+64<<20); err != nil {
		return fmt.Errorf("self-update: %w", err)
	}

	// 备份（方言感知：SQLite VACUUM INTO / mysqldump / pg_dump；失败即中止）
	bc, err := loadBootstrap(*confDir)
	if err != nil {
		return err
	}
	backupDir := updater.BackupPath(binPath, orDev(Version))
	if err := update.BackupDatabase(ctx, bc.Data, backupDir); err != nil {
		return fmt.Errorf("self-update: 备份失败，中止更新: %w", err)
	}
	// 二进制一并入备份目录（DB 恢复 + 二进制回退双保险）
	_ = copyFile(binPath, filepath.Join(backupDir, "zcard.old"))

	// 下载 → 验签落位（落盘流式，124MB 大产物零内存，方案 §8）
	newPath := filepath.Join(filepath.Dir(binPath), "zcard.new")
	if err := downloadToFile(ctx, cli, m, assetName, newPath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("self-update: %w", err)
	}
	if err := updater.ApplyFile(binPath, orDev(Version), m.Version, newPath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("self-update: %w", err)
	}
	fmt.Printf("已更新到 %s（备份: %s）\n", m.Version, backupDir)

	// 重启：systemd 安装态自动；失败给手动指引（新版启动后健康门自动确认/回滚）
	if err := updater.RestartService("zcard"); err != nil {
		fmt.Println("!! " + err.Error())
	}
	return nil
}

// runGenKey 生成更新密钥对（发行侧离线保管私钥；公钥 -X 注入或 -pubkey 过渡）。
func runGenKey() error {
	pub, priv, err := updater.GenerateKeyPair()
	if err != nil {
		return err
	}
	fmt.Printf("公钥（编译注入 -X updater.DefaultPublicKeyHex）:\n  %s\n", hex.EncodeToString(pub))
	fmt.Printf("私钥（离线保管，泄露即换钥发版）:\n  %s\n", hex.EncodeToString(priv))
	return nil
}

// runSign 扫描 dist 目录产物并生成签名清单 update.json（CI 发版步骤）。
func runSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyFile := fs.String("key", "", "私钥文件（hex；或 ZCARD_SIGN_KEY 环境变量）")
	dir := fs.String("dir", "dist", "产物目录")
	version := fs.String("version", "", "版本 tag（vX.Y.Z，必填）")
	channel := fs.String("channel", "stable", "通道 stable|beta")
	notesFile := fs.String("notes-file", "", "changelog markdown 文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" || !updater.IsSemver(*version) {
		return fmt.Errorf("sign: --version 必须为 vX.Y.Z semver")
	}
	keyHex := *keyFile
	if keyHex == "" {
		keyHex = os.Getenv("ZCARD_SIGN_KEY")
	} else {
		raw, err := os.ReadFile(keyHex)
		if err != nil {
			return err
		}
		keyHex = strings.TrimSpace(string(raw))
	}
	if keyHex == "" {
		return fmt.Errorf("sign: 缺少私钥（--key 或 ZCARD_SIGN_KEY）")
	}
	rawKey, err := hex.DecodeString(keyHex)
	if err != nil || len(rawKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("sign: 私钥格式非法")
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		return err
	}
	var files []updater.FileEntry
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == "update.json" || !strings.HasPrefix(name, "zcard-") {
			continue
		}
		p := filepath.Join(*dir, name)
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		sum, size, err := updater.HashFile(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		files = append(files, updater.FileEntry{Name: name, SHA256: sum, Size: size})
	}
	if len(files) == 0 {
		return fmt.Errorf("sign: 目录 %s 无 zcard-* 产物", *dir)
	}
	notes := ""
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err != nil {
			return err
		}
		notes = string(b)
	}
	out, err := updater.SignManifest(ed25519.PrivateKey(rawKey), *version, *channel, notes, files)
	if err != nil {
		return err
	}
	dst := filepath.Join(*dir, "update.json")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("已签名 %d 个产物 → %s\n", len(files), dst)
	return nil
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func copyFile(src, dst string) error {
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

// downloadToFile 流式落盘下载（DownloadAsset 边写边哈希校验；失败文件由调用方删）。
func downloadToFile(ctx context.Context, cli *updater.Client, m *updater.Manifest, name, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := cli.DownloadAsset(ctx, m, name, f, func(received, total int64) {
		if total > 0 {
			fmt.Printf("\r下载中 %d/%d MB", received>>20, total>>20)
		}
	}); err != nil {
		_ = f.Close()
		return err
	}
	fmt.Println()
	return f.Close()
}
