package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// Exercise the actual visit writer, aggregation and chart service together, so
// differing storage/API date formats cannot silently turn recorded visits into zero.
func TestTrafficRecordedVisits(t *testing.T) {
	d := newDashboardData(t)
	tracker := audit.NewTrackRepo(d)
	svc := &AdminDashboardService{traffic: tracker}
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{SubsiteID: 5})
	today := time.Now().UTC()
	for _, ip := range []string{"192.0.2.1", "192.0.2.1", "192.0.2.2"} {
		tracker.RecordVisit(ctx, 5, "/products", 0, ip)
	}
	tracker.RecordVisit(ctx, 0, "/products", 0, "192.0.2.3")
	for _, offset := range []int{1, 6, 13, 29, 30} {
		d.Client.PageView.Create().SetSubsiteID(5).
			SetDay(today.AddDate(0, 0, -offset).Format("20060102")).
			SetPath("/products").SetIP("192.0.2.1").SaveX(ctx)
	}
	for _, days := range []int32{0, 7, 14, 30, 99} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			reply, err := svc.GetTraffic(ctx, &adminv1.GetTrafficRequest{Days: days})
			if err != nil {
				t.Fatal(err)
			}
			count := int(days)
			if count != 14 && count != 30 {
				count = 7
			}
			if len(reply.Points) != count {
				t.Fatalf("points = %d, want %d", len(reply.Points), count)
			}
			for i, point := range reply.Points {
				offset := count - 1 - i
				date := today.AddDate(0, 0, -offset).Format("2006-01-02")
				var pv, uv int64
				switch offset {
				case 0:
					pv, uv = 3, 2
				case 1, 6, 13, 29:
					pv, uv = 1, 1
				}
				if point.Date != date || point.Pv != pv || point.Uv != uv {
					t.Errorf("point = %v, want %s PV=%d UV=%d", point, date, pv, uv)
				}
			}
		})
	}
}
