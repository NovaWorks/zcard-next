// Package theme installs and serves compiled, responsive storefront themes.
package theme

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Root is persistent runtime data, independent of the embedded web build.
const Root = "data/themes"
const LegacyRoot = "web/storefront/templates"
const MaxZipBytes = 20 << 20
const MaxExtractBytes = 100 << 20
const maxFiles = 4000

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Meta is the theme.json contract. SchemaVersion defaults to 1 for older packages.
type Meta struct {
	Key            string `json:"key,omitempty"`
	Name           string `json:"name"`
	Desc           string `json:"desc,omitempty"`
	Preview        string `json:"preview,omitempty"`
	Author         string `json:"author,omitempty"`
	Version        string `json:"version"`
	SchemaVersion  int    `json:"schema_version,omitempty"`
	SettingsSchema string `json:"settings_schema,omitempty"`
}

type Theme struct {
	Meta
	Dir      string
	Revision string
}

func (t *Theme) BaseURL() string { return "/templates/" + t.Key + "/" + t.Revision + "/" }
func ValidKey(key string) bool   { return keyPattern.MatchString(key) && key != "classic" }

// Install publishes an immutable revision only after validating the entire ZIP.
// A single atomic pointer replacement lists it as the latest installed revision.
// Activation is a separate settings transaction. Old revisions remain available
// so an already-open page can still load its original lazy chunks after an upgrade.
func Install(raw []byte) (*Theme, error) {
	if len(raw) > MaxZipBytes {
		return nil, fmt.Errorf("主题包超过 20MB 上限")
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("主题文件必须是有效 ZIP 压缩包")
	}
	if len(z.File) > maxFiles {
		return nil, fmt.Errorf("主题包文件数量超过 %d 个", maxFiles)
	}
	if err = os.MkdirAll(Root, 0755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(Root, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	seen := map[string]bool{}
	var total int64
	for _, f := range z.File {
		n := f.Name
		if strings.HasPrefix(n, "__MACOSX/") || path.Base(n) == ".DS_Store" {
			continue
		}
		if strings.Contains(n, "\\") || strings.HasPrefix(n, "/") || strings.Contains(n, "\x00") || len(n) > 512 {
			return nil, fmt.Errorf("主题包含非法路径")
		}
		clean := path.Clean(strings.TrimSuffix(n, "/"))
		if clean == "." || clean != strings.TrimSuffix(n, "/") || strings.HasPrefix(clean, "../") || clean == ".." {
			return nil, fmt.Errorf("主题包含越界或非规范路径")
		}
		for _, part := range strings.Split(clean, "/") {
			if strings.HasPrefix(part, ".") {
				return nil, fmt.Errorf("主题不能包含隐藏文件或目录")
			}
		}
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return nil, fmt.Errorf("主题包含非法文件类型")
		}
		if seen[clean] {
			return nil, fmt.Errorf("主题包存在重复路径")
		}
		seen[clean] = true
		if f.FileInfo().IsDir() {
			continue
		}
		if !allowedFile(clean) {
			return nil, fmt.Errorf("主题仅支持编译后的静态文件，不支持 %s", path.Ext(clean))
		}
		if f.UncompressedSize64 > uint64(MaxExtractBytes-total) {
			return nil, fmt.Errorf("主题解压后超过 100MB 上限")
		}
		target := filepath.Join(stage, filepath.FromSlash(clean))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		in, e := f.Open()
		if e != nil {
			return nil, e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			in.Close()
			return nil, e
		}
		nbytes, e := io.Copy(out, io.LimitReader(in, MaxExtractBytes-total+1))
		closeErr := out.Close()
		in.Close()
		total += nbytes
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if total > MaxExtractBytes {
			return nil, fmt.Errorf("主题解压后超过 100MB 上限")
		}
	}
	base := stage
	key := ""
	entries, err := os.ReadDir(stage)
	if err != nil {
		return nil, err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		key = entries[0].Name()
		base = filepath.Join(stage, key)
	}
	t, err := inspect(base, key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	t.Revision = fmt.Sprintf("%x", sum)
	parent := filepath.Join(Root, t.Key)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return nil, err
	}
	dest := filepath.Join(parent, t.Revision)
	// Content-addressed directories can be safely reused by concurrent identical installs.
	if err = os.Rename(base, dest); err != nil {
		if _, statErr := os.Stat(dest); statErr != nil {
			return nil, err
		}
	}
	pointer, err := os.CreateTemp(parent, ".current-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(pointer.Name())
	if _, err = pointer.WriteString(t.Revision); err == nil {
		err = pointer.Sync()
	}
	closeErr := pointer.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = os.Rename(pointer.Name(), filepath.Join(parent, "current")); err != nil {
		return nil, err
	}
	t.Dir = dest
	return t, nil
}

func inspect(dir, key string) (*Theme, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	meta, err := root.ReadFile("theme.json")
	if err != nil {
		return nil, fmt.Errorf("主题根目录缺少 theme.json 清单")
	}
	if len(meta) > 64<<10 {
		return nil, fmt.Errorf("theme.json 过大")
	}
	var m Meta
	if err = json.Unmarshal(meta, &m); err != nil {
		return nil, fmt.Errorf("theme.json 不是合法 JSON")
	}
	if key == "" {
		key = m.Key
	} else if m.Key != "" && m.Key != key {
		return nil, fmt.Errorf("theme.json 的 key 必须与主题目录名一致")
	}
	if !ValidKey(key) {
		return nil, fmt.Errorf("主题 key 仅允许小写字母、数字、_-，长度 1–64，classic 为内置保留名称")
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "" {
		return nil, fmt.Errorf("theme.json 必须填写 name 和 version")
	}
	if m.SettingsSchema != "" {
		if m.SettingsSchema != "settings.schema.json" {
			return nil, fmt.Errorf("设置定义文件须为 settings.schema.json")
		}
		if _, err := LoadSettings(&Theme{Meta: m, Dir: dir}); err != nil {
			return nil, err
		}
	}
	if m.SchemaVersion != 0 && m.SchemaVersion != 1 {
		return nil, fmt.Errorf("不支持的主题 schema_version，请使用 1")
	}
	m.Key = key
	b, err := root.ReadFile("index.html")
	if err != nil {
		return nil, fmt.Errorf("主题根目录缺少编译后的 index.html，请上传构建产物而非源码")
	}
	if _, err = indexHeadEnd(b); err != nil {
		return nil, err
	}
	if err = validateIndexAssets(root, b); err != nil {
		return nil, err
	}
	if m.Preview != "" {
		if !fs.ValidPath(m.Preview) || !imageFile(m.Preview) {
			return nil, fmt.Errorf("preview 必须是包内图片的相对路径")
		}
		f, e := root.Open(m.Preview)
		if e != nil {
			return nil, fmt.Errorf("主题预览图不存在")
		}
		st, e := f.Stat()
		f.Close()
		if e != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("主题预览图非法")
		}
	}
	return &Theme{Meta: m, Dir: dir}, nil
}

func validateIndexAssets(root *os.Root, b []byte) error {
	z := html.NewTokenizer(bytes.NewReader(b))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return nil
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		for _, attr := range tok.Attr {
			if attr.Key != "src" && !(tok.Data == "link" && attr.Key == "href") {
				continue
			}
			u, err := url.Parse(attr.Val)
			if err != nil {
				return fmt.Errorf("index.html 包含非法资源地址")
			}
			if u.IsAbs() || u.Host != "" || u.Path == "" {
				continue
			}
			if strings.HasPrefix(u.Path, "/") {
				return fmt.Errorf("主题资源必须使用相对路径（Vite base: './'），发现 %s", u.Path)
			}
			name := strings.TrimPrefix(u.Path, "./")
			if !fs.ValidPath(name) || !allowedFile(name) {
				return fmt.Errorf("主题资源路径非法：%s", name)
			}
			f, err := root.Open(name)
			if err != nil {
				return fmt.Errorf("主题包缺少 index.html 引用的资源：%s", name)
			}
			st, err := f.Stat()
			f.Close()
			if err != nil || !st.Mode().IsRegular() {
				return fmt.Errorf("主题资源不是普通文件：%s", name)
			}
		}
	}
}

// indexHeadEnd also rejects a competing base URL; the server injects the immutable
// asset prefix before any scripts or styles. Vite themes must build with base './'.
func indexHeadEnd(b []byte) (int, error) {
	if len(b) > 2<<20 {
		return 0, fmt.Errorf("index.html 超过 2MB 上限")
	}
	z := html.NewTokenizer(bytes.NewReader(b))
	offset, head := 0, 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		offset += len(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		if tok.Data == "base" {
			return 0, fmt.Errorf("index.html 不可设置 base 标签；构建时请使用相对资源路径")
		}
		if tok.Data == "head" && head == 0 {
			head = offset
		}
	}
	if head == 0 {
		return 0, fmt.Errorf("index.html 必须包含 head 标签")
	}
	return head, nil
}

// Selection pins the storefront to an explicitly activated package revision.
type Selection struct {
	Key      string `json:"key"`
	Revision string `json:"revision,omitempty"`
}

// Resolve returns the latest installed package, for listing and activation only.
func Resolve(key string) (*Theme, error) {
	if !ValidKey(key) {
		return nil, fmt.Errorf("invalid theme key")
	}
	if b, err := os.ReadFile(filepath.Join(Root, key, "current")); err == nil {
		return ResolveRevision(key, strings.TrimSpace(string(b)))
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return ResolveRevision(key, "legacy")
}

// ResolveRevision never follows the installed pointer: uploading cannot change it.
func ResolveRevision(key, revision string) (*Theme, error) {
	if !ValidKey(key) {
		return nil, fmt.Errorf("invalid theme key")
	}
	var dir string
	if revision == "legacy" {
		dir = filepath.Join(LegacyRoot, key)
	} else {
		if !revisionPattern.MatchString(revision) {
			return nil, fmt.Errorf("invalid theme revision")
		}
		dir = filepath.Join(Root, key, revision)
	}
	t, err := inspect(dir, key)
	if err != nil {
		return nil, err
	}
	t.Revision = revision
	return t, nil
}
func List() ([]*Theme, error) {
	keys := map[string]bool{}
	for _, dir := range []string{Root, LegacyRoot} {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() && ValidKey(e.Name()) {
				keys[e.Name()] = true
			}
		}
	}
	out := []*Theme{}
	for key := range keys {
		if t, e := Resolve(key); e == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
