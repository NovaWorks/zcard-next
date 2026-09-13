package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/audit"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
)

func TestBeijingMidnightOverviewTrendAndSettle(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	midnight := time.Date(2026, 9, 12, 16, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return midnight.Add(time.Minute) }
	seedOrder(t, d, 0, "paid", 100, midnight.Add(-time.Second))
	seedOrder(t, d, 0, "paid", 200, midnight)
	seedOrder(t, d, 0, "paid", 300, midnight.Add(-6*24*time.Hour+time.Hour))
	today, yesterday, _, _, _, _, e := r.GetOverview(ctx)
	if e != nil || today.Orders != 1 || today.Revenue != 200 || yesterday.Revenue != 100 {
		t.Fatalf("midnight: %+v %+v %v", today, yesterday, e)
	}
	points, e := r.GetTrend(ctx, 7)
	if e != nil || points[0].Revenue != 300 || points[6].Date != "2026-09-13" || points[6].Revenue != 200 || points[5].Revenue != 100 {
		t.Fatalf("trend: %+v %v", points, e)
	}
	if e = r.RunDailySettle(ctx, midnight.Add(-time.Second)); e != nil {
		t.Fatal(e)
	}
	stats, e := r.GetDailyStats(ctx, 0, "20260912", "20260912")
	if e != nil || len(stats) != 1 || stats[0].Amount != 100 {
		t.Fatal("settle double-counted midnight", stats, e)
	}
}
func TestLegacyUTCVisitDatesAreRebucketed(t *testing.T) {
	d := newDashboardData(t)
	ctx := context.Background()
	start := businessday.Start(time.Now())
	tracker := audit.NewTrackRepo(d)
	for i, at := range []time.Time{start.Add(-time.Second), start, start.Add(time.Second)} {
		d.Client.PageView.Create().SetDay(at.Format("20060102")).SetCreatedAt(at).SetIP("192.0.2.1").SetPath("/").SetUserID(uint64(i)).SaveX(ctx)
	}
	rows, e := tracker.TrafficByDay(ctx, 0, 2)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 || rows[0].PV != 1 || rows[1].PV != 2 || rows[1].UV != 1 {
		t.Fatalf("legacy buckets: %+v", rows)
	}
}

// Starting after the former midnight window still settles yesterday, once per day.
func TestDailySettleCronCatchesLateStart(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 4, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	seedOrder(t, d, 0, "paid", 100, now.Add(-24*time.Hour))
	run := r.DailySettleCron()
	run(ctx)
	rows, err := r.GetDailyStats(ctx, 0, "20260912", "20260912")
	if err != nil || len(rows) != 1 || rows[0].Amount != 100 {
		t.Fatalf("late start: %+v %v", rows, err)
	}
	seedOrder(t, d, 0, "paid", 200, now.Add(-23*time.Hour))
	run(ctx)
	rows, _ = r.GetDailyStats(ctx, 0, "20260912", "20260912")
	if rows[0].Amount != 100 {
		t.Fatal("same-day cron reran")
	}
	now = now.Add(24 * time.Hour)
	run(ctx)
	rows, err = r.GetDailyStats(ctx, 0, "20260913", "20260913")
	if err != nil || len(rows) != 1 {
		t.Fatalf("next day not settled %+v %v", rows, err)
	}
}
