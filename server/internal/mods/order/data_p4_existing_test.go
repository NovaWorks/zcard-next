package order

import (
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

func TestP4ExistingOrderAfterMembershipChangeAndDisable(t *testing.T) {
	e := p2Fixture(t, "sqlite")
	in := e.input(e.allowed.ID)
	in.IdempotencyKey = "p4-existing"
	original, err := e.uc.CreateOrder(e.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	e.d.Client.User.UpdateOneID(e.allowed.ID).SetManualLevelID(e.d.Client.User.GetX(e.ctx, e.denied.ID).ManualLevelID).ExecX(e.ctx)
	c := e.command
	c.Action = "disable"
	c.TargetDigest = ""
	c.ExpectedGeneration = 2
	c.OperationID = p2UUID()
	if _, err = e.manager.Operate(e.ctx, c); err != nil {
		t.Fatal(err)
	}
	replay, err := e.uc.CreateOrder(e.ctx, in)
	if err != nil || replay.OrderNo != original.OrderNo {
		t.Fatal("existing replay changed", err)
	}
	in.IdempotencyKey = "p4-new"
	if _, err = e.uc.CreateOrder(e.ctx, in); err == nil {
		t.Fatal("new request bypassed disabled gate")
	}
	if err = e.uc.MarkPaid(e.ctx, original.OrderNo); err != nil {
		t.Fatal(err)
	}
	before := p2Snapshot(t, e.d)
	if err = e.uc.MarkPaid(e.ctx, original.OrderNo); err != nil {
		t.Fatal(err)
	}
	if before != p2Snapshot(t, e.d) {
		t.Fatal("duplicate payment changed business state")
	}
	svc := NewStoreOrderService(e.uc, nil)
	owner := identity.WithClaims(e.ctx, &authn.Claims{Subject: e.allowed.ID, Realm: authn.RealmUser})
	if got, err := svc.GetOrder(owner, &storefrontv1.GetOrderRequest{OrderNo: original.OrderNo}); err != nil || got.OrderNo != original.OrderNo {
		t.Fatal("old paid order inaccessible", err)
	}
	other := identity.WithClaims(e.ctx, &authn.Claims{Subject: e.denied.ID, Realm: authn.RealmUser})
	if _, err := svc.GetOrder(other, &storefrontv1.GetOrderRequest{OrderNo: original.OrderNo}); err == nil {
		t.Fatal("cross-account order access")
	}
}
