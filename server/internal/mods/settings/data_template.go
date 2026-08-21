package settings

// 模板清单与主题安装（WP 主题式选择的数据源）：
// 模板目录约定 web/storefront/templates/<key>/theme.json——
//   {"name": "显示名", "desc": "描述", "preview": "preview.png 或 /uploads/xxx.png",
//    "author": "作者", "version": "1.0.0"}
// 兼容旧 meta.json（同结构子集）；目录缺失（默认部署仅单一 SPA）回退内置 classic 清单。
// 主题安装 = zip base64 上传 → 安全解压校验 → 原子落盘（覆盖升级）。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// templatesRoot 模板目录（相对运行目录；fullstack 单二进制 web/ 与二进制同目录）。
const templatesRoot = "web/storefront/templates"

// 主题安装限额（防 zip bomb）：包体 20MB、解压后 100MB。
const (
	maxThemeZipBytes     = 20 * 1024 * 1024
	maxThemeExtractBytes = 100 * 1024 * 1024
)

// builtinTemplates 内置清单（默认部署兜底）。
var builtinTemplates = []*adminv1.TemplateItem{
	{Key: "classic", Name: "Classic", Desc: "经典模板（默认）", Preview: "", Author: "ZCard Team", Version: "1.0.0"},
}

type themeMeta struct {
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Preview string `json:"preview"`
	Author  string `json:"author"`
	Version string `json:"version"`
}

// ListTemplates 可用模板清单。
func (s *AdminSettingsService) ListTemplates(_ context.Context, _ *emptypb.Empty) (*adminv1.TemplateList, error) {
	items, err := scanTemplates()
	if err != nil {
		return nil, err
	}
	return &adminv1.TemplateList{Templates: items}, nil
}

// scanTemplates 扫描模板目录（theme.json 优先，兼容 meta.json）。
func scanTemplates() ([]*adminv1.TemplateItem, error) {
	dirs, err := os.ReadDir(templatesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return append([]*adminv1.TemplateItem(nil), builtinTemplates...), nil
		}
		return nil, err
	}
	out := make([]*adminv1.TemplateItem, 0)
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		item := readThemeMeta(d.Name())
		if item == nil {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return append([]*adminv1.TemplateItem(nil), builtinTemplates...), nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// readThemeMeta 读取单主题元数据（theme.json 优先，meta.json 兼容）。
func readThemeMeta(key string) *adminv1.TemplateItem {
	base := filepath.Join(templatesRoot, key)
	var data []byte
	for _, name := range []string{"theme.json", "meta.json"} {
		if b, err := os.ReadFile(filepath.Join(base, name)); err == nil {
			data = b
			break
		}
	}
	if len(data) == 0 {
		return nil
	}
	var meta themeMeta
	_ = json.Unmarshal(data, &meta)
	item := &adminv1.TemplateItem{
		Key:     key,
		Name:    meta.Name,
		Desc:    meta.Desc,
		Preview: meta.Preview,
		Author:  meta.Author,
		Version: meta.Version,
	}
	if item.Name == "" {
		item.Name = key
	}
	if item.Version == "" {
		item.Version = "1.0.0"
	}
	// 相对路径预览图 → 模板静态服务 URL（/templates/<key>/<preview>）
	if item.Preview != "" && !strings.HasPrefix(item.Preview, "/") {
		item.Preview = fmt.Sprintf("/templates/%s/%s", key, strings.TrimPrefix(item.Preview, "./"))
	}
	return item
}

// templateKeyExists 模板 key 是否在可用清单内（UpdateSetting 写入校验）。
func templateKeyExists(key string) bool {
	items, err := scanTemplates()
	if err != nil {
		return false
	}
	for _, it := range items {
		if it.Key == key {
			return true
		}
	}
	return false
}

// themeKeyRe 主题目录名（即 key）白名单：防路径注入。
var themeKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// InstallTemplate 安装主题：base64 zip → 安全解压 → 结构校验 → 原子落盘。
// 安全纪律：zip-slip 防护（Clean 后必须仍在临时目录内）、拒符号链接、
// 包体/解压总量双上限、key 白名单、临时目录校验通过才 rename。
func (s *AdminSettingsService) InstallTemplate(_ context.Context, req *adminv1.InstallTemplateRequest) (*adminv1.TemplateItem, error) {
	raw, err := base64.StdEncoding.DecodeString(req.GetDataBase64())
	if err != nil {
		return nil, errors.BadRequest("settings.TEMPLATE_BAD_ENCODING", "主题文件编码非法")
	}
	if len(raw) > maxThemeZipBytes {
		return nil, errors.BadRequest("settings.TEMPLATE_TOO_LARGE", "主题包超过 20MB 上限")
	}
	if len(raw) < 4 || !bytes.Equal(raw[:4], []byte("PK\x03\x04")) {
		return nil, errors.BadRequest("settings.TEMPLATE_NOT_ZIP", "主题文件必须是 zip 压缩包")
	}

	// 解压到临时目录（同根下 .install-* 隐藏目录；MkdirTemp 不建多级父目录）
	if err := os.MkdirAll(templatesRoot, 0o755); err != nil {
		return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "创建模板目录失败")
	}
	tmpDir, err := os.MkdirTemp(templatesRoot, ".install-")
	if err != nil {
		return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "创建安装目录失败")
	}
	defer os.RemoveAll(tmpDir)

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, errors.BadRequest("settings.TEMPLATE_NOT_ZIP", "zip 压缩包解析失败")
	}
	var extracted int64
	hasThemeJSON := false
	var themeName, themeVersion string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// 拒符号链接/设备等特殊文件
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return nil, errors.BadRequest("settings.TEMPLATE_INVALID", "主题包含非法文件类型")
		}
		// zip-slip：Clean 后必须仍在临时目录内
		target := filepath.Join(tmpDir, f.Name)
		if !strings.HasPrefix(target, tmpDir+string(os.PathSeparator)) {
			return nil, errors.BadRequest("settings.TEMPLATE_PATH_ESCAPE", "主题包含越界路径")
		}
		// 解压总量上限（zip bomb 防护）
		if extracted+int64(f.UncompressedSize64) > maxThemeExtractBytes {
			return nil, errors.BadRequest("settings.TEMPLATE_TOO_LARGE", "主题解压后超过 100MB 上限")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "创建主题目录失败")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, errors.BadRequest("settings.TEMPLATE_INVALID", "主题包读取失败")
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "写入主题文件失败")
		}
		n, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			return nil, errors.BadRequest("settings.TEMPLATE_INVALID", "主题包解压失败")
		}
		extracted += n
		if filepath.Base(target) == "theme.json" && filepath.Dir(target) != tmpDir {
			hasThemeJSON = true
			if meta, err := os.ReadFile(target); err == nil {
				var m themeMeta
				if json.Unmarshal(meta, &m) == nil {
					themeName, themeVersion = m.Name, m.Version
				}
			}
		}
	}
	if !hasThemeJSON {
		return nil, errors.BadRequest("settings.TEMPLATE_NO_MANIFEST", "主题包缺少 theme.json 清单")
	}

	// key = zip 内唯一顶层目录名
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "读取主题目录失败")
	}
	var key string
	for _, e := range entries {
		if e.IsDir() {
			key = e.Name()
			break
		}
	}
	if key == "" || !themeKeyRe.MatchString(key) {
		return nil, errors.BadRequest("settings.TEMPLATE_BAD_KEY", "主题目录名非法（仅小写字母/数字/_-）")
	}

	// 原子落盘：把主题子目录 tmpDir/<key> 移到目标（目标存在先删→覆盖升级）
	target := filepath.Join(templatesRoot, key)
	if err := os.RemoveAll(target); err != nil {
		return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "清理旧主题失败")
	}
	if err := os.Rename(filepath.Join(tmpDir, key), target); err != nil {
		return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "安装主题失败")
	}
	item := readThemeMeta(key)
	if item == nil {
		return nil, errors.InternalServer("settings.TEMPLATE_INSTALL_FAILED", "主题元数据读取失败")
	}
	if themeName != "" {
		item.Name = themeName
	}
	if themeVersion != "" {
		item.Version = themeVersion
	}
	return item, nil
}
