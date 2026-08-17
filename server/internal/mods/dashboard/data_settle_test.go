package dashboard

// P3-07 日结测试：聚合落表幂等（重跑覆盖）/历史查询/分站隔离。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	_ "modernc.org/sqlite"
)

func newDashboardData(t *testing.T) *data.Data {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:dashsettle%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
}

func seedOrder(t *testing.T, d *data.Data, subsite uint64, status string, amount int64, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.Client.Order.Create().
		SetOrderNo(fmt.Sprintf("S-%d-%d", subsite, at.UnixNano())).
		SetSubsiteID(subsite).
		SetStatus(order.Status(status)).
		SetTotalAmount(amount).
		SetBaseCurrency("CNY").
		SetCreatedAt(at).
		SetVersion(0).
		Save(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestRunDailySettle 日结聚合落表 + 重跑覆盖幂等 + 历史查询。
func TestRunDailySettle(t *testing.T) {
	d := newDashboardData(t)
	repo := NewDashboardRepoImpl(d)
	ctx := context.Background()
	day := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)

	// 主站 2 单（1 paid 1000 + 1 pending）+ 分站 5 单（1 paid 2000）
	seedOrder(t, d, 0, "paid", 1000, day.Add(2*time.Hour))
	seedOrder(t, d, 0, "pending_payment", 500, day.Add(3*time.Hour))
	seedOrder(t, d, 5, "paid", 2000, day.Add(4*time.Hour))
	seedOrder(t, d, 5, "delivered", 3000, day.Add(5*time.Hour))
	seedOrder(t, d, 5, "canceled", 999, day.Add(6*time.Hour))

	if err := repo.RunDailySettle(ctx, day); err != nil {
		t.Fatal(err)
	}
	// 主站：orders=2 amount=1000 paid=1
	main, err := repo.GetDailyStats(ctx, 0, "20260816", "20260816")
	if err != nil || len(main) != 1 {
		t.Fatalf("主站日结错误: %+v %v", main, err)
	}
	if main[0].Orders != 2 || main[0].Amount != 1000 || main[0].Paid != 1 {
		t.Fatalf("主站日结数值错误: %+v", main[0])
	}
	// 分站：orders=3 amount=5000 paid=2
	sub, err := repo.GetDailyStats(ctx, 5, "20260816", "20260816")
	if err != nil || len(sub) != 1 {
		t.Fatalf("分站日结错误: %+v %v", sub, err)
	}
	if sub[0].Orders != 3 || sub[0].Amount != 5000 || sub[0].Paid != 2 {
		t.Fatalf("分站日结数值错误: %+v", sub[0])
	}
	// 重跑覆盖幂等（追加一单后重跑，行数不变、值更新）
	seedOrder(t, d, 0, "paid", 700, day.Add(7*time.Hour))
	if err := repo.RunDailySettle(ctx, day); err != nil {
		t.Fatal(err)
	}
	main2, _ := repo.GetDailyStats(ctx, 0, "20260816", "20260816")
	if len(main2) != 1 || main2[0].Orders != 3 || main2[0].Amount != 1700 {
		t.Fatalf("重跑覆盖错误: %+v", main2)
	}
	// daily_stats 总行数 = 2 租户 × 3 指标 = 6
	total, _ := d.Client.DailyStat.Query().Count(ctx)
	if total != 6 {
		t.Fatalf("日结行数错误: %d", total)
	}
}

// TestDailySettleSubsiteIsolation 分站视角隔离（GetOverview 只统计本站）。
func TestDailySettleSubsiteIsolation(t *testing.T) {
	d := newDashboardData(t)
	repo := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := time.Now().UTC()
	seedOrder(t, d, 0, "paid", 1000, now.Add(-time.Hour))
	seedOrder(t, d, 5, "paid", 2000, now.Add(-2*time.Hour))

	// 主站视角：只见主站 1 单
	mainCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 0, IsMain: true})
	today, _, _, err := repo.GetOverview(mainCtx)
	if err != nil || today.Orders != 1 || today.Revenue != 1000 {
		t.Fatalf("主站视角隔离错误: %+v %v", today, err)
	}
	// 分站视角：只见本站 1 单
	subCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 5, IsMain: false})
	today2, _, _, err := repo.GetOverview(subCtx)
	if err != nil || today2.Orders != 1 || today2.Revenue != 2000 {
		t.Fatalf("分站视角隔离错误: %+v %v", today2, err)
	}
}
