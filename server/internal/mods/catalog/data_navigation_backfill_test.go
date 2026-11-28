package catalog

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
	"testing"
)

func TestBackfillNavigationOnlyForMissingExactBinding(t *testing.T) {
	d, r := newMaintainEnv(t)
	ctx := context.Background()
	a, _, _ := seedUpstream(t, d)
	for _, row := range []struct {
		conn       uint64
		code, page string
	}{{8, "A", "111"}, {9, "B", "222"}, {9, "A", "not-numeric"}, {9, "A", "101"}, {9, "A", "999"}} {
		if err := r.BackfillUpstreamNavigation(ctx, row.conn, row.code, row.page); err != nil {
			t.Fatal(err)
		}
	}
	got := d.Client.Product.GetX(ctx, a.ID)
	if upstreamsource.PageID(got.UpstreamNavigation, 9, "A") != "101" || got.Price != a.Price || got.Status != a.Status {
		t.Fatalf("navigation changed existing data: %+v", got)
	}
	if d.Client.OrderItem.Query().CountX(ctx) != 0 {
		t.Fatal("backfill changed orders")
	}
}
