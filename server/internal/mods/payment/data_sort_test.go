package payment

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"testing"
)

func TestChannelSortPreservedAndZero(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	s := NewAdminPaymentService(r, d)
	ctx := context.Background()
	ch, err := s.CreateChannel(ctx, &adminv1.CreateChannelRequest{Name: "sorted", Code: "sorted", Driver: "wallet", Enabled: true, Sort: 30})
	if err != nil {
		t.Fatal(err)
	}
	ch, err = s.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, Name: "renamed"})
	if err != nil || ch.Sort != 30 || !ch.Enabled {
		t.Fatalf("partial update lost state: %v %v", ch, err)
	}
	ch, err = s.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, Sort: checkoutPtr(int32(0))})
	if err != nil || ch.Sort != 0 || !ch.Enabled {
		t.Fatalf("explicit zero: %v %v", ch, err)
	}
}
