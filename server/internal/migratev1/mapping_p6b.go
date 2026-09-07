package migratev1

// P6 内容/日志域：media（行 + 文件本体复制到 2.0 StorageRoot）、
// visit_logs 聚合迁移（90 天窗口，pv 聚合 / uv 近似）、security_audit_logs（12 个月窗口）。
// 映射规格《数据迁移工具开发计划》§5.9。

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/hex"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/securityauditlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media"
)

// MigrateContent P6 内容/日志域。
func (m *Migrator) MigrateContent(ctx context.Context) error {
	if err := m.migrateMedia(ctx); err != nil {
		return err
	}
	if err := m.migrateVisitLogs(ctx); err != nil {
		return err
	}
	return m.migrateAuditLogs(ctx)
}

// migrateMedia 1.x media/media_categories → media + 文件复制。
// 文件源：<OldStorage>/storage/app/public/<path>（--old-storage 指定 1.x 部署根）；
// 缺失文件不阻断（清单进报告）。sha256 迁移时计算补齐。
func (m *Migrator) migrateMedia(ctx context.Context) error {
	var (
		id                     int64
		categoryID, size       sql.NullInt64
		width, height          sql.NullInt64
		originalName, filename string
		pathCol, url, mimeType string
		deletedAt              sql.NullString
		createdAt              sql.NullString
	)
	oldRoot := m.Opts.OldStorageRoot
	return m.scanTable(ctx, "media",
		[]string{"id", "category_id", "original_name", "filename", "path", "url",
			"mime_type", "size", "width", "height", "deleted_at", "created_at"},
		func() []any {
			return []any{&id, &categoryID, &originalName, &filename, &pathCol, &url,
				&mimeType, &size, &width, &height, &deletedAt, &createdAt}
		},
		func(int64) error {
			if nullStr(deletedAt) != "" {
				m.st.Record("media", "skip") // 软删媒体不迁
				return nil
			}
			if _, ok := m.IDs.Get(ctx, "media", uint64(id)); ok {
				m.st.Record("media", "skip")
				return nil
			}
			// 文件复制（源缺失不阻断）
			rel := strings.TrimPrefix(strings.ReplaceAll(pathCol, "\\", "/"), "/")
			dst := filepath.Join(mediaStorageRoot(), filepath.FromSlash(rel))
			sum := ""
			if oldRoot != "" {
				src := filepath.Join(oldRoot, "storage", "app", "public", filepath.FromSlash(rel))
				data, err := os.ReadFile(src)
				if err != nil {
					m.RW.AddError("media", uint64(id), "文件缺失未复制："+rel)
				} else {
					if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
						return err
					}
					if err := os.WriteFile(dst, data, 0o644); err != nil {
						return err
					}
					sh := sha256.Sum256(data)
					sum = hex.EncodeToString(sh[:])
				}
			} else {
				m.RW.AddError("media", uint64(id), "未提供 --old-storage，文件未复制（仅迁行）："+rel)
			}
			b := m.Client.Media.Create().
				SetPath(rel).
				SetName(originalName).
				SetMime(mimeType).
				SetSize(nullInt(size))
			if sum != "" {
				b.SetSha256(sum)
			}
			if w := nullInt(width); w > 0 {
				b.SetWidth(int32(w))
			}
			if h := nullInt(height); h > 0 {
				b.SetHeight(int32(h))
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			md, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "media", uint64(id), md.ID); err != nil {
				return err
			}
			m.st.Record("media", "migrated")
			return nil
		},
	)
}

func mediaStorageRoot() string { return media.StorageRoot }

// migrateVisitLogs 1.x 原始 PV → 2.0 小时聚合行（uv 近似 = pv，报告注明口径）。
func (m *Migrator) migrateVisitLogs(ctx context.Context) error {
	t := m.st.table("visit_logs")
	if m.dry {
		var n int64
		if err := m.Src.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `visit_logs`").Scan(&n); err != nil {
			return err
		}
		t.Planned = n
		return nil
	}
	days := m.Opts.VisitDays
	if days == 0 {
		return nil
	}
	since := time.Now().AddDate(0, 0, -days).UTC()
	rows, err := m.Src.DB.QueryContext(ctx,
		"SELECT DATE_FORMAT(`created_at`, '%Y%m%d'), CAST(HOUR(`created_at`) AS SIGNED), COALESCE(`path`, ''), COUNT(*) "+
			"FROM `visit_logs` WHERE `created_at` >= ? GROUP BY 1, 2, 3", since.Format("2006-01-02 15:04:05"))
	if err != nil {
		// sqlite 单测源无 DATE_FORMAT——退化为按天聚合（单测路径）
		return m.migrateVisitLogsByDay(ctx, since)
	}
	defer rows.Close()
	var migrated int64
	for rows.Next() {
		var (
			date string
			hour int64
			path string
			pv   int64
		)
		if err := rows.Scan(&date, &hour, &path, &pv); err != nil {
			return err
		}
		if _, err := m.Client.VisitLog.Create().
			SetStatDate(date).
			SetStatHour(int8(hour)).
			SetPath(truncateRunes(path, 255)).
			SetPv(pv).
			SetUv(pv). // 原始 PV 无访客标识，uv 近似（报告口径）
			Save(ctx); err != nil {
			return err
		}
		migrated++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	t.Migrated = migrated
	m.RW.AddError("visit_logs", 0, fmt.Sprintf("聚合 %d 行（近 %d 天，uv=pv 近似口径）", migrated, days))
	return nil
}

// migrateVisitLogsByDay sqlite 兼容路径（仅单测）：按天聚合，hour=0。
func (m *Migrator) migrateVisitLogsByDay(ctx context.Context, since time.Time) error {
	rows, err := m.Src.DB.QueryContext(ctx,
		"SELECT strftime('%Y%m%d', `created_at`), COALESCE(`path`, ''), COUNT(*) "+
			"FROM `visit_logs` WHERE `created_at` >= ? GROUP BY 1, 2",
		since.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil // 源无 visit_logs（可选项）
	}
	defer rows.Close()
	var migrated int64
	for rows.Next() {
		var (
			date string
			path string
			pv   int64
		)
		if err := rows.Scan(&date, &path, &pv); err != nil {
			return err
		}
		if _, err := m.Client.VisitLog.Create().
			SetStatDate(date).
			SetStatHour(0).
			SetPath(truncateRunes(path, 255)).
			SetPv(pv).
			SetUv(pv).
			Save(ctx); err != nil {
			return err
		}
		migrated++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	m.st.table("visit_logs").Migrated = migrated
	return nil
}

// migrateAuditLogs 1.x security_audit_logs → security_audit_logs（窗口迁移）。
// 1.x 列差异（source/method/path/status_code/user_agent）→ metadata 保真。
func (m *Migrator) migrateAuditLogs(ctx context.Context) error {
	months := m.Opts.AuditMonths
	if months == -1 {
		return nil
	}
	var (
		id, actorID, statusCode int64
		source, action          string
		targetType              sql.NullString
		targetID                sql.NullString
		method                  sql.NullString
		pathCol                 sql.NullString
		ip                      sql.NullString
		userAgent               sql.NullString
		metadata                sql.NullString
		createdAt               sql.NullString
	)
	scan := func(query string, args ...any) error {
		rows, err := m.Src.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return nil // 源无此表（可选迁移）
		}
		defer rows.Close()
		for rows.Next() {
			if err := rows.Scan(&id, &actorID, &source, &action, &targetType, &targetID,
				&method, &pathCol, &statusCode, &ip, &userAgent, &metadata, &createdAt); err != nil {
				return err
			}
			if _, ok := m.IDs.Get(ctx, "security_audit_logs", uint64(id)); ok {
				m.st.Record("security_audit_logs", "skip")
				continue
			}
			actorType := "user"
			switch source {
			case "admin":
				actorType = "admin"
			case "system":
				actorType = "system"
			}
			meta := map[string]any{
				"v1_source":     source,
				"v1_method":     nullStr(method),
				"v1_path":       nullStr(pathCol),
				"v1_status":     statusCode,
				"v1_user_agent": nullStr(userAgent),
			}
			if tt := nullStr(targetType); tt != "" {
				meta["v1_target_type"] = tt
			}
			if md := nullStr(metadata); md != "" {
				meta["v1_metadata"] = md
			}
			b := m.Client.SecurityAuditLog.Create().
				SetActorType(securityauditlog.ActorType(actorType)).
				SetAction(action)
			if actorID > 0 {
				b.SetActorID(uint64(actorID))
			}
			if p := nullStr(ip); p != "" {
				b.SetIP(p)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			} else {
				continue
			}
			if _, err := b.SetMetadata(meta).Save(ctx); err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "security_audit_logs", uint64(id), uint64(id)); err != nil {
				return err
			}
			m.st.Record("security_audit_logs", "migrated")
		}
		return rows.Err()
	}
	if months == 0 {
		return scan("SELECT `id`, `actor_id`, `source`, `action`, `target_type`, `target_id`, `method`, `path`, `status_code`, `ip`, `user_agent`, `metadata`, `created_at` FROM `security_audit_logs` ORDER BY `id`")
	}
	since := time.Now().AddDate(0, -months, 0).UTC().Format("2006-01-02 15:04:05")
	return scan("SELECT `id`, `actor_id`, `source`, `action`, `target_type`, `target_id`, `method`, `path`, `status_code`, `ip`, `user_agent`, `metadata`, `created_at` FROM `security_audit_logs` WHERE `created_at` >= ? ORDER BY `id`", since)
}
