//go:build integration

package testint

import (
	"context"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/dashboard"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
)

func TestSupportStatsMySQL(t *testing.T) { runSupportStats(MySQL(t)) }
func TestSupportStatsPG(t *testing.T)    { runSupportStats(PG(t)) }
func runSupportStats(h *Harness) {
	t, ctx, c := h.T, context.Background(), h.Data.Client
	start := businessday.Start(time.Now())
	for i, at := range []time.Time{start.Add(-time.Second), start, start.Add(time.Second)} {
		c.PageView.Create().SetDay(at.Format("20060102")).SetCreatedAt(at).SetIP("192.0.2.1").SetPath("/").SetUserID(uint64(i)).SaveX(ctx)
	}
	rows, err := audit.NewTrackRepo(h.Data).TrafficByDay(ctx, 0, 2)
	if err != nil || len(rows) != 2 || rows[0].PV != 1 || rows[1].PV != 2 || rows[1].UV != 1 {
		t.Fatalf("traffic %+v %v", rows, err)
	}
	c.Order.Create().SetOrderNo("before-midnight").SetStatus(order.StatusPaid).SetTotalAmount(100).SetCreatedAt(start.Add(-time.Second)).SaveX(ctx)
	c.Order.Create().SetOrderNo("at-midnight").SetStatus(order.StatusPaid).SetTotalAmount(200).SetCreatedAt(start).SaveX(ctx)
	repo := dashboard.NewDashboardRepoImpl(h.Data)
	if err = repo.RunDailySettle(ctx, start.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	date := businessday.Date(start.Add(-time.Second), "20060102")
	stats, err := repo.GetDailyStats(ctx, 0, date, date)
	if err != nil || len(stats) != 1 || stats[0].Amount != 100 {
		t.Fatalf("midnight settle %+v %v", stats, err)
	}
	parent := c.Category.Create().SetName("parent").SaveX(ctx)
	child := c.Category.Create().SetName("child").SetParentID(parent.ID).SaveX(ctx)
	c.Product.Create().SetName("own").SetSlug("own").SetCategoryID(child.ID).SetStatus(1).SaveX(ctx)
	c.Product.Create().SetName("deleted").SetSlug("deleted").SetCategoryID(child.ID).SetStatus(-1).SaveX(ctx)
	c.Product.Create().SetName("foreign").SetSlug("foreign").SetCategoryID(child.ID).SetSubsiteID(9).SetStatus(1).SaveX(ctx)
	counts, err := catalog.NewProductRepoImpl(h.Data, nil).CategoryProductCounts(ctx, c.Category.Query().AllX(ctx))
	if err != nil || counts[parent.ID] != 1 || counts[child.ID] != 1 {
		t.Fatalf("counts %+v %v", counts, err)
	}
}
