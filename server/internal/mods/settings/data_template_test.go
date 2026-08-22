package settings

// 模板清单/主题安装测试：zip 正常安装、覆盖升级、zip-slip/非 zip/缺清单拒绝、
// theme.json 与 meta.json 双格式扫描。模板目录经 t.Chdir 隔离到临时目录。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
)

// buildZip 内存构造 zip（entries: path → content）。
func buildZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func newTemplateEnv(t *testing.T) *AdminSettingsService {
	t.Helper()
	t.Chdir(t.TempDir()) // 模板目录相对 cwd，隔离到临时目录
	return &AdminSettingsService{}
}

const themeJSON = `{"name":"暗夜主题","desc":"深色风格","preview":"preview.png","author":"ZCard Team","version":"2.1.0"}`

// TestInstallTemplateOK 正常安装：解压落盘 + 清单可扫 + 元数据完整 + 预览图 URL 拼接。
func TestInstallTemplateOK(t *testing.T) {
	svc := newTemplateEnv(t)
	zipData := buildZip(t, map[string]string{
		"dark/theme.json":    themeJSON,
		"dark/preview.png":   "fake-png",
		"dark/assets/a.css":  "body{}",
		"dark/assets/b.js":   "console.log(1)",
		"dark/extra/note.md": "readme",
	})

	item, err := svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{DataBase64: zipData})
	if err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}
	if item.Key != "dark" || item.Name != "暗夜主题" || item.Version != "2.1.0" || item.Author != "ZCard Team" {
		t.Fatalf("安装返回元数据错误: %+v", item)
	}
	if item.Preview != "/templates/dark/preview.png" {
		t.Fatalf("预览图 URL 拼接错误: %s", item.Preview)
	}

	items, err := scanTemplates()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.Key == "dark" {
			found = true
			if it.Version != "2.1.0" || it.Author != "ZCard Team" {
				t.Fatalf("扫描元数据不完整: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("安装后清单中找不到 dark 主题")
	}
}

// TestInstallTemplateOverwrite 覆盖升级：同 key 二次安装替换旧版本。
func TestInstallTemplateOverwrite(t *testing.T) {
	svc := newTemplateEnv(t)
	zip1 := buildZip(t, map[string]string{"dark/theme.json": `{"name":"v1","version":"1.0.0"}`})
	if _, err := svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{DataBase64: zip1}); err != nil {
		t.Fatalf("首次安装: %v", err)
	}
	zip2 := buildZip(t, map[string]string{"dark/theme.json": `{"name":"v2","version":"2.0.0"}`})
	if _, err := svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{DataBase64: zip2}); err != nil {
		t.Fatalf("覆盖安装: %v", err)
	}
	items, _ := scanTemplates()
	var count int
	for _, it := range items {
		if it.Key == "dark" {
			count++
			if it.Version != "2.0.0" {
				t.Fatalf("覆盖后版本错误: %+v", it)
			}
		}
	}
	if count != 1 {
		t.Fatalf("覆盖后 dark 出现 %d 次, want 1", count)
	}
}

// TestInstallTemplateZipSlip 拒绝越界路径（../ 逃逸）。
func TestInstallTemplateZipSlip(t *testing.T) {
	svc := newTemplateEnv(t)
	zipData := buildZip(t, map[string]string{
		"evil/theme.json": themeJSON,
		"../escape.txt":   "pwned",
	})
	_, err := svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{DataBase64: zipData})
	if err == nil {
		t.Fatal("zip-slip 未被拒绝")
	}
}

// TestInstallTemplateNotZip 拒绝非 zip 数据。
func TestInstallTemplateNotZip(t *testing.T) {
	svc := newTemplateEnv(t)
	_, err := svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{
		DataBase64: base64.StdEncoding.EncodeToString([]byte("not a zip file at all")),
	})
	if err == nil {
		t.Fatal("非 zip 未被拒绝")
	}
}

// TestInstallTemplateNoManifest 拒绝缺 theme.json 的包。
func TestInstallTemplateNoManifest(t *testing.T) {
	svc := newTemplateEnv(t)
	zipData := buildZip(t, map[string]string{"nomanifest/preview.png": "png"})
	_, err := svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{DataBase64: zipData})
	if err == nil {
		t.Fatal("缺 theme.json 未被拒绝")
	}
}

// TestScanTemplatesMetaCompat 兼容旧 meta.json 格式扫描。
func TestScanTemplatesMetaCompat(t *testing.T) {
	newTemplateEnv(t)
	writeFile(t, "web/storefront/templates/legacy/meta.json", `{"name":"旧主题","desc":"老格式","preview":"legacy.png"}`)
	writeFile(t, "web/storefront/templates/modern/theme.json", themeJSON)

	items, err := scanTemplates()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]*adminv1.TemplateItem{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	legacy := byKey["legacy"]
	if legacy == nil || legacy.Name != "旧主题" || legacy.Version != "1.0.0" || legacy.Preview != "/templates/legacy/legacy.png" {
		t.Fatalf("meta.json 兼容扫描错误: %+v", legacy)
	}
	modern := byKey["modern"]
	if modern == nil || modern.Author != "ZCard Team" || modern.Version != "2.1.0" {
		t.Fatalf("theme.json 扫描错误: %+v", modern)
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := writeAll(name, content); err != nil {
		t.Fatal(err)
	}
}

// writeAll MkdirAll + WriteFile（相对 cwd）。
func writeAll(name, content string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, []byte(content), 0o644)
}
