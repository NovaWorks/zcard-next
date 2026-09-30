package procurement

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
	"testing"
)

func TestSMSOwnerTenantAndOperationIsolation(t *testing.T) {
	s, d, row := smsEnv(t)
	api := NewStoreSMSService(s)
	base := context.Background()
	d.Client.Order.UpdateOneID(row.OrderID).SetUserID(1).ExecX(base)
	d.Client.SMSIntent.UpdateOneID(row.ID).SetLeaseUntil(0).SetCanCancel(true).SetCanFinish(true).ExecX(base)
	no := d.Client.Order.GetX(base, row.OrderID).OrderNo
	owner := identity.WithClaims(base, &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	for _, ctx := range []context.Context{base, identity.WithClaims(base, &authn.Claims{Subject: 2, Realm: authn.RealmUser}), identity.WithClaims(base, &authn.Claims{Subject: 1, Realm: authn.RealmAdmin}), tenancy.WithContext(owner, tenancy.Context{SubsiteID: 99})} {
		if _, e := api.GetSMS(ctx, &storefrontv1.SMSOrderRequest{OrderNo: no}); e == nil {
			t.Fatal("foreign access accepted")
		}
		if _, e := api.ActSMS(ctx, &storefrontv1.SMSActionRequest{OrderNo: no, Action: "cancel", RequestId: uuid.NewString()}); e == nil {
			t.Fatal("foreign operation accepted")
		}
	}
	req := &storefrontv1.SMSActionRequest{OrderNo: no, Action: "cancel", RequestId: uuid.NewString()}
	for i := 0; i < 2; i++ {
		if _, e := api.ActSMS(owner, req); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.SMSOperation.Query().CountX(base) != 1 {
		t.Fatal("duplicate operation")
	}
	req.Action = "finish"
	if _, e := api.ActSMS(owner, req); e == nil {
		t.Fatal("same id changed action")
	}
	req.RequestId = uuid.NewString()
	if _, e := api.ActSMS(owner, req); e == nil {
		t.Fatal("parallel actions accepted")
	}
}

func TestSMSConfirmedRefundSupersedesActionWarning(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("original").SetAction("cancel").SetStatus("review").SaveX(ctx)
	row = d.Client.SMSIntent.UpdateOneID(row.ID).SetPhase("refund").SetRetailRefundState("pending").SaveX(ctx)
	got, e := NewStoreSMSService(s).reply(ctx, row)
	if e != nil || got.OperationStatus != "" || got.Message != "供货退款已确认，正在退还本次实际支付金额" {
		t.Fatal("obsolete action warning obscures confirmed refund", e, got)
	}
}
