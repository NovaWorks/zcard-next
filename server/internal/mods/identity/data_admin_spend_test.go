package identity

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"testing"
)

func TestMemberCompletedOrdersKeepSpending(t *testing.T) {
	_, d := newRegCodeEnv(t)
	ctx := context.Background()
	u := d.Client.User.Create().SetUsername("linked-buyer").SetEmail("buyer@example.test").SaveX(ctx)
	s := NewAdminUserManageService(&UserRepo{data: d}, nil)
	for i := 0; i < 4; i++ {
		d.Client.Order.Create().SetOrderNo(fmt.Sprintf("linked-%d", i)).SetUserID(u.ID).SetStatus("paid").SetTotalAmount(1000).SaveX(ctx)
	}
	d.Client.Order.Create().SetOrderNo("guest-same-email").SetUserID(0).SetGuestContact("buyer@example.test").SetStatus("paid").SetTotalAmount(9900).SaveX(ctx)
	before, err := s.GetUser(ctx, &adminv1.GetUserRequest{Id: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if before.User.OrderCount != 4 || before.User.SpentCents != 4000 {
		t.Fatal(before.User)
	}
	t.Logf("same email guest order excluded; member paid: user_id=%d orders=%d spent=%d", u.ID, before.User.OrderCount, before.User.SpentCents)
	for _, o := range d.Client.Order.Query().AllX(ctx) {
		if o.UserID == u.ID {
			d.Client.Order.UpdateOneID(o.ID).SetStatus("completed").ExecX(ctx)
		}
	}
	after, err := s.GetUser(ctx, &adminv1.GetUserRequest{Id: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if after.User.OrderCount != 4 || after.User.SpentCents != 4000 {
		t.Fatal(after.User)
	}
	t.Logf("only status changed, same member user_id=%d orders=%d spent=%d", u.ID, after.User.OrderCount, after.User.SpentCents)
}
