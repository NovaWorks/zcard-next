package audit

// T5 访问统计明细：page_views（PV/UV 逐请求明细）+ user_sessions（在线心跳）。
// 埋点原则：统计性质数据，写入失败一律忽略、绝不阻断业务请求。

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pageview"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/usersession"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
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
		SetDay(now.Format("20060102")).
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
	start := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("20060102")
	client := data.Client(ctx, r.data)
	var pv []struct {
		Day   string
		Count int64
	}
	if err := client.PageView.Query().
		Where(pageview.DayGTE(start), pageview.SubsiteID(subsite)).
		GroupBy(pageview.FieldDay).
		Aggregate(ent.Count()).
		Scan(ctx, &pv); err != nil {
		return nil, err
	}
	// UV = 按 (day, ip) 分组后内存按日去重（ent 无 COUNT(DISTINCT) 聚合）
	var pairs []struct {
		Day string
		IP  string
	}
	if err := client.PageView.Query().
		Where(pageview.DayGTE(start), pageview.SubsiteID(subsite)).
		GroupBy(pageview.FieldDay, pageview.FieldIP).
		Scan(ctx, &pairs); err != nil {
		return nil, err
	}
	uvByDay := map[string]int64{}
	for _, row := range pairs {
		uvByDay[row.Day]++
	}
	byDay := make(map[string]*auditport.TrafficDay, len(pv))
	for _, row := range pv {
		byDay[row.Day] = &auditport.TrafficDay{Date: row.Day, PV: row.Count, UV: uvByDay[row.Day]}
	}
	out := make([]auditport.TrafficDay, 0, len(byDay))
	for _, t := range byDay {
		out = append(out, *t)
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
