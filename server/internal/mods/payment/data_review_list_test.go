package payment

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"testing"
	"time"
)

func TestReviewListKeepsDeletedOrderReceiptsAndTenantScope(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("deleted-review").SetStatus("canceled").SetAdminDeletedAt(time.Now().UTC()).SaveX(ctx)
	p := d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("be").SetAmount(100).SetStatus("success").SetReviewReason("迟到到账").SaveX(ctx)
	d.Client.Payment.Create().SetChannel("be").SetAmount(200).SetStatus("success").SaveX(ctx)
	other := d.Client.Payment.Create().SetSubsiteID(9).SetChannel("be").SetAmount(900).SetStatus("success").SetReviewReason("其他分站").SaveX(ctx)
	rows, err := repo.ListPayments(ctx, "", "", 0, 20, true)
	if err != nil || len(rows) != 1 || rows[0].ID != p.ID {
		t.Fatalf("main=%+v %v", rows, err)
	}
	rows, err = repo.ListPayments(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9}), "", "", 0, 20, true)
	if err != nil || len(rows) != 1 || rows[0].ID != other.ID {
		t.Fatalf("tenant=%+v %v", rows, err)
	}
}
