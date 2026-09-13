package audit

// T5 访问统计明细：page_views（PV/UV 逐请求明细）+ user_sessions（在线心跳）。
// 埋点原则：统计性质数据，写入失败一律忽略、绝不阻断业务请求。

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pageview"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/usersession"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
)

// TrackRepo 访问埋点/统计仓储。
type TrackRepo struct {
	data *data.Data
}

// NewTrackRepo 构造。
func NewTrackRepo(d *data.Data) *TrackRepo {
	return &TrackRepo{data: d}
}

// RecordVisit 记一次访问：PV 明细一行 + 登录用户在线心跳 upsert。
// 失败忽略（埋点不阻断请求）。
func (r *TrackRepo) RecordVisit(ctx context.Context, subsite uint64, path string, userID uint64, ip string) {
	now := time.Now().UTC()
	client := data.Client(ctx, r.data)
	_, err := client.PageView.Create().
		SetSubsiteID(subsite).
		SetDay(businessday.Date(now, "20060102")).
		SetPath(path).
		SetUserID(userID).
		SetIP(ip).
		Save(ctx)
	if err != nil || userID == 0 {
		return
	}
	// 在线心跳 upsert（唯一键 subsite_id + user_id；created_at 不可变保留）
	_ = client.UserSession.Create().
		SetSubsiteID(subsite).
		SetUserID(userID).
		SetIP(ip).
		SetLastActiveAt(now).
		OnConflictColumns(usersession.FieldSubsiteID, usersession.FieldUserID).
		UpdateNewValues().
		Exec(ctx)
}

// CountOnlineUsers 在线用户数（last_active_at ≥ since 且分站隔离）。
func (r *TrackRepo) CountOnlineUsers(ctx context.Context, subsite uint64, since time.Time) (int64, error) {
	n, err := data.Client(ctx, r.data).UserSession.Query().
		Where(usersession.LastActiveAtGTE(since), usersession.SubsiteID(subsite)).
		Count(ctx)
	return int64(n), err
}

// TrafficByDay 近 N 天 PV/UV（PV=行数，UV=按 ip 去重；缺日补零由调用方补齐）。
func (r *TrackRepo) TrafficByDay(ctx context.Context, subsite uint64, days int) ([]auditport.TrafficDay, error) {
	if days < 1 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	// Use absolute request timestamps to repair old UTC day labels without rewriting history.
	start := businessday.Start(businessday.Now()).In(businessday.Location).AddDate(0, 0, -(days - 1))
	client := data.Client(ctx, r.data)
	out := make([]auditport.TrafficDay, 0, days)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		end := day.AddDate(0, 0, 1)
		var counts []struct {
			PV int64 `json:"pv"`
			UV int64 `json:"uv"`
		}
		err := client.PageView.Query().Where(pageview.SubsiteID(subsite),
			pageview.DayGTE(day.AddDate(0, 0, -1).Format("20060102")), pageview.DayLTE(day.Format("20060102")),
			pageview.CreatedAtGTE(day.UTC()), pageview.CreatedAtLT(end.UTC())).Aggregate(
			func(s *sql.Selector) string { return sql.As(sql.Count("*"), "pv") },
			func(s *sql.Selector) string { return sql.As(sql.Count(sql.Distinct(s.C(pageview.FieldIP))), "uv") },
		).Scan(ctx, &counts)
		if err != nil {
			return nil, err
		}
		if len(counts) > 0 {
			out = append(out, auditport.TrafficDay{Date: day.Format("20060102"), PV: counts[0].PV, UV: counts[0].UV})
		}
	}

	return out, nil
}

// CleanupVisitData 清理过期数据（cron）：明细保留 90 天，心跳保留 24 小时。
func (r *TrackRepo) CleanupVisitData(ctx context.Context) error {
	client := data.Client(ctx, r.data)
	dayCut := time.Now().UTC().AddDate(0, 0, -90).Format("20060102")
	if _, err := client.PageView.Delete().Where(pageview.DayLT(dayCut)).Exec(ctx); err != nil {
		return err
	}
	activeCut := time.Now().UTC().Add(-24 * time.Hour)
	_, err := client.UserSession.Delete().Where(usersession.LastActiveAtLT(activeCut)).Exec(ctx)
	return err
}
