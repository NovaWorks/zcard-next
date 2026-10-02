package supply

// 上游商品图采集（cover 落本地）：同步时把上游封面下载到本地 uploads，
// 避免相对路径（acg 站 /assets/cache/images/...）在本地店面渲染 404。
//
// 目录按渠道名分组（uploads/<渠道名>/，重名渠道自动加 2/3……；解析结果
// 持久化到连接 settings.cover_dir，重启沿用）；失败 fail-open：返回完整
// 上游 URL（上游可直连时仍可显示），不阻断同步。同一服务内按 URL 去重。

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
)

// maxCoverBytes 封面大小上限（2MB——发卡商品图足够；Content-Length 不可信时 LimitReader 兜底）。
const maxCoverBytes = 2 << 20

var coverClient = httpx.NewSafeClient(10 * time.Second)

// resolveUpstreamURL resolves media relative to the upstream directory. Unsafe
// schemes and embedded credentials are never retained as browser image URLs.
func resolveUpstreamURL(baseURL, cover string) string {
	cover = strings.TrimSpace(cover)
	if cover == "" || strings.Contains(cover, "\\") || strings.HasPrefix(cover, "#") {
		return ""
	}
	u, err := url.Parse(cover)
	if err != nil || u.User != nil {
		return ""
	}
	if !u.IsAbs() {
		base, err := url.Parse(strings.TrimSpace(baseURL))
		if err != nil || base.User != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") {
			return ""
		}
		if !strings.HasSuffix(base.Path, "/") {
			base.Path += "/"
			if base.RawPath != "" {
				base.RawPath += "/"
			}
		}
		base.RawQuery = ""
		base.Fragment = ""
		u = base.ResolveReference(u)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

// downloadCover 下载上游封面到本地 uploads/<coverDir>/；返回本地 URL（/uploads/...）。
// 任何失败均 fail-open：返回完整上游 URL（记录 warn，不阻断同步）。
func (s *SyncService) downloadCover(ctx context.Context, baseURL, cover, coverDir string) string {
	src := resolveUpstreamURL(baseURL, cover)
	if src == "" {
		return ""
	}
	key := coverDir + "|" + src
	// 去重缓存（并发任务加锁）
	s.coverMu.Lock()
	if s.coverCache == nil {
		s.coverCache = map[string]string{}
	}
	if got, ok := s.coverCache[key]; ok {
		s.coverMu.Unlock()
		return got
	}
	s.coverMu.Unlock()

	local := s.fetchCover(ctx, src, coverDir, upstreamOrigin(baseURL))
	// A temporary upstream failure must remain retryable on the next sync.
	if strings.HasPrefix(local, "/uploads/") {
		s.coverMu.Lock()
		s.coverCache[key] = local
		s.coverMu.Unlock()
	}
	return local
}

func upstreamOrigin(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}).String()
}

// Image query strings may carry upstream access tokens. Diagnostics retain only
// the origin and path, while the browser fallback keeps the usable full URL.
func mediaLogURL(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return "<invalid>"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func mediaFetchError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// fetchCover 单次下载 + 落盘（无缓存）。
func (s *SyncService) fetchCover(ctx context.Context, src, coverDir, referer string) string {
	if ctx.Err() != nil {
		return src
	}
	if err := httpx.ValidateURL(src); err != nil {
		s.log.Warn("supply.cover_fetch_blocked", "url", mediaLogURL(src), "err", mediaFetchError(err))
		return src
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return src
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := coverClient.Do(req)
	if err != nil {
		s.log.Warn("supply.cover_fetch_failed", "url", mediaLogURL(src), "err", mediaFetchError(err))
		return src
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("supply.cover_fetch_status", "url", mediaLogURL(src), "status", resp.StatusCode)
		return src
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil || len(body) > maxCoverBytes {
		s.log.Warn("supply.cover_fetch_too_large", "url", mediaLogURL(src))
		return src
	}
	ext := coverExt(resp.Header.Get("Content-Type"), src)
	if ext == "" {
		s.log.Warn("supply.cover_fetch_unsupported", "url", mediaLogURL(src), "type", resp.Header.Get("Content-Type"))
		return src
	}
	rel, err := media.SaveLocalIn(coverDir, body, ext)
	if err != nil {
		s.log.Warn("supply.cover_save_failed", "url", mediaLogURL(src), "err", err)
		return src
	}
	return "/uploads/" + rel
}

// coverExt permits filename fallback only when the server omitted the MIME or
// used a generic binary MIME. An HTML/login page at a .png URL is not an image.
func coverExt(ctype, src string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(ctype, ";")[0])) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "", "application/octet-stream", "binary/octet-stream":
		// Some suppliers serve valid images without an image Content-Type.
	default:
		return ""
	}
	switch strings.ToLower(path.Ext(strings.Split(src, "?")[0])) {
	case ".jpg", ".jpeg":
		return ".jpg"
	case ".png":
		return ".png"
	case ".webp":
		return ".webp"
	case ".gif":
		return ".gif"
	}
	return ""
}

// allocateCoverDir 从已占用目录名集合中分配唯一名：name 空闲用 name，否则
// name2/name3……（首个空闲）。name 净化后为空则返回空（年月目录兜底）。
func allocateCoverDir(existing map[string]bool, name string) string {
	if name == "" {
		return ""
	}
	if !existing[name] {
		return name
	}
	for i := 2; i < 100; i++ {
		candidate := name + itoa(i)
		if !existing[candidate] {
			return candidate
		}
	}
	return ""
}

// itoa 小整数转十进制。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// listUploadSubDirs 扫描 uploads/ 一级目录名（不存在返回空集）。
func listUploadSubDirs() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(media.StorageRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// sanitizeSubDir 渠道名 → 目录名（与 media.sanitizeSubDir 同规则：中文/字母/
// 数字/._- 保留，其余替换为 -；截断 60；空返回空）。
func sanitizeSubDir(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// deleteLocalCover 删除本地封面文件（cover 为 /uploads/ 路径时；其余忽略）。
func deleteLocalCover(cover string) {
	if !strings.HasPrefix(cover, "/uploads/") {
		return
	}
	rel := strings.TrimPrefix(cover, "/uploads/")
	rel = strings.ReplaceAll(rel, "\\", "/")
	_ = media.DeleteLocal(rel)
}
