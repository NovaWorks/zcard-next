package media

// 本地存储 + 静态服务（）：
// 存储根 data/uploads/YYYY/MM/<随机名>.<ext>（1.x 年月目录惯例）；
// 静态路由 /uploads/：扩展名 Content-Type + ETag（sha256 前 16 位）+
// Cache-Control；目录列表禁用（仅精确文件路径）；路径穿越拒绝。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// StorageRoot 本地存储根（相对工作目录；Docker 部署挂 /data 卷）。
var StorageRoot = "data/uploads"

// contentTypeByExt 静态服务 Content-Type（仅白名单扩展——其余一律 octet-stream 拒绝）。
var contentTypeByExt = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".png": "image/png", ".webp": "image/webp", ".gif": "image/gif",
}

// SaveLocal 存储：净化后字节 → 年月目录随机名；返回相对路径。
func SaveLocal(clean []byte, ext string) (relPath string, err error) {
	return SaveLocalIn("", clean, ext)
}

// SaveLocalIn 存储到 uploads/<subDir>/（subDir 净化：仅中文/字母/数字/._-，
// 其余替换为 -；空 = 年月目录）。返回相对路径（正斜杠——DB 与 URL 统一口径）。
// 子目录用于按来源分组（如采集渠道名），便于定位与清理。
func SaveLocalIn(subDir string, clean []byte, ext string) (relPath string, err error) {
	sub := sanitizeSubDir(subDir)
	now := time.Now().UTC()
	dir := StorageRoot
	if sub != "" {
		dir = filepath.Join(dir, sub)
	} else {
		dir = filepath.Join(dir, fmt.Sprintf("%04d/%02d", now.Year(), int(now.Month())))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("media: 创建存储目录失败: %w", err)
	}
	name := randToken() + ext
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, clean, 0o644); err != nil {
		return "", fmt.Errorf("media: 写入文件失败: %w", err)
	}
	rel := strings.TrimPrefix(strings.ReplaceAll(full, "\\", "/"), StorageRoot+"/")
	return rel, nil
}

// sanitizeSubDir 子目录名净化：保留中文/字母/数字/._-，其余替换为 -；禁路径穿越。
func sanitizeSubDir(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r >= 0x4e00 && r <= 0x9fff: // 中文
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return ""
	}
	if len(out) > 60 { // 目录名上限（防超长）
		out = out[:60]
	}
	return out
}

// DeleteLocal 物理删除（ref=0 时调用；文件缺失视为已清理）。
func DeleteLocal(relPath string) error {
	full, ok := safeJoin(relPath)
	if !ok {
		return nil // 非法路径：忽略（不泄露存在性）
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// safeJoin 相对路径拼接存储根（拒绝穿越）。
func safeJoin(relPath string) (string, bool) {
	clean := path.Clean("/" + relPath) // 归一化并锚定根
	full := filepath.Join(StorageRoot, clean)
	rootAbs, err1 := filepath.Abs(StorageRoot)
	fullAbs, err2 := filepath.Abs(full)
	if err1 != nil || err2 != nil || !strings.HasPrefix(fullAbs, rootAbs+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}

// randToken 随机文件名（16 字节 hex）。
func randToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败不可恢复（熵源故障）——fail closed
		panic("media: 随机数生成失败")
	}
	return hex.EncodeToString(b)
}

// HashFile sha256（秒传去重/ETag 复用）。
func HashFile(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// RegisterStatic 静态服务 /uploads/（kratos srv.HandleFunc 注册）。
// ETag = sha256(path)+size（内容变更即失效）；仅白名单扩展；无目录列表。
func RegisterStatic(mux *khttp.Server) {
	mux.HandlePrefix("/uploads/", http.HandlerFunc(serveStatic))
}

func serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/uploads/")
	if rel == "" || strings.HasSuffix(rel, "/") {
		http.NotFound(w, r) // 目录列表禁用
		return
	}
	ext := strings.ToLower(path.Ext(rel))
	ct, ok := contentTypeByExt[ext]
	if !ok {
		http.NotFound(w, r) // 非白名单扩展（含 .php 等）一律 404
		return
	}
	full, ok := safeJoin(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	etag := fmt.Sprintf(`"%x-%x"`, st.ModTime().UnixNano(), st.Size())
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // 随机名内容不变
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	http.ServeFile(w, r, full) // ServeFile 拒绝目录；内容类型已显式设置
}
