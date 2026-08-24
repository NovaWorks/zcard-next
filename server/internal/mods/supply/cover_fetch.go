package supply

// 上游商品图采集（cover 落本地）：同步时把上游封面下载到本地 uploads，
// 避免相对路径（acg 站 /assets/cache/images/...）在本地店面渲染 404。
// 失败 fail-open：返回完整上游 URL（上游可直连时仍可显示），不阻断同步。
// 同一服务内按源 URL 去重（多任务共享缓存，避免重复下载）。

import (
	"context"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
)

// maxCoverBytes 封面大小上限（2MB——发卡商品图足够；Content-Length 不可信时 LimitReader 兜底）。
const maxCoverBytes = 2 << 20

var coverClient = &http.Client{Timeout: 10 * time.Second}

// resolveUpstreamURL 封面相对路径 → 完整 URL（baseURL 拼接；已是完整 URL 原样返回）。
func resolveUpstreamURL(baseURL, cover string) string {
	if cover == "" {
		return ""
	}
	if strings.HasPrefix(cover, "http://") || strings.HasPrefix(cover, "https://") {
		return cover
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(cover, "/")
}

// downloadCover 下载上游封面到本地 uploads；返回本地 URL（/uploads/...）。
// 任何失败均 fail-open：返回完整上游 URL（记录 warn，不阻断同步）。
func (s *SyncService) downloadCover(ctx context.Context, baseURL, cover string) string {
	if cover == "" {
		return ""
	}
	src := resolveUpstreamURL(baseURL, cover)
	// 去重缓存（并发任务加锁）
	s.coverMu.Lock()
	if s.coverCache == nil {
		s.coverCache = map[string]string{}
	}
	if got, ok := s.coverCache[cover]; ok {
		s.coverMu.Unlock()
		return got
	}
	s.coverMu.Unlock()

	local := s.fetchCover(ctx, src)
	s.coverMu.Lock()
	s.coverCache[cover] = local
	s.coverMu.Unlock()
	return local
}

// fetchCover 单次下载 + 落盘（无缓存）。
func (s *SyncService) fetchCover(ctx context.Context, src string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return src
	}
	resp, err := coverClient.Do(req)
	if err != nil {
		s.log.Warn("supply.cover_fetch_failed", "url", src, "err", err)
		return src
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("supply.cover_fetch_status", "url", src, "status", resp.StatusCode)
		return src
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil || len(body) > maxCoverBytes {
		s.log.Warn("supply.cover_fetch_too_large", "url", src)
		return src
	}
	ext := coverExt(resp.Header.Get("Content-Type"), src)
	if ext == "" {
		s.log.Warn("supply.cover_fetch_unsupported", "url", src, "type", resp.Header.Get("Content-Type"))
		return src
	}
	rel, err := media.SaveLocal(body, ext)
	if err != nil {
		s.log.Warn("supply.cover_save_failed", "url", src, "err", err)
		return src
	}
	return "/uploads/" + rel
}

// coverExt Content-Type → 白名单扩展名（缺失时按 URL 后缀兜底；未知返回空）。
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
