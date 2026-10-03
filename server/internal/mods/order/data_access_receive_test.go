package order

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"strconv"
	"testing"
)

func TestGuestPhysicalReceiveRequiresOrderCredential(t *testing.T) {
	d, uc, _, r := guestPhysicalAccess(t)
	ctx := context.Background()
	o := d.Client.Order.Query().OnlyX(ctx)
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	d.Client.Order.UpdateOneID(o.ID).SetStatus("delivered").SetShippingStatus("shipped").ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(it.ID).SetShippedQuantity(it.Quantity).SetFulfillmentStatus("shipped").ExecX(ctx)
	shipment := d.Client.Shipment.Create().SetOrderID(o.ID).SetAdminID(1).SetCarrier("Test").SetTrackingNo("TEST001").SetAddress(o.ShippingAddress).SetItems(map[string]int32{strconv.FormatUint(it.ID, 10): it.Quantity}).SetRequestKey("receive-test-request").SaveX(ctx)
	svc := NewStoreOrderService(uc, nil)
	for _, token := range []string{"", "invalid"} {
		if _, err := svc.ReceiveShipment(ctx, &storefrontv1.ReceiveShipmentRequest{OrderNo: r.OrderNo, ShipmentId: shipment.ID, OrderAccessToken: token}); err == nil {
			t.Fatal("unauthorized receive accepted")
		}
	}
	if d.Client.OrderItem.GetX(ctx, it.ID).ReceivedQuantity != 0 {
		t.Fatal("unauthorized receive changed item")
	}
	if _, err := svc.ReceiveShipment(ctx, &storefrontv1.ReceiveShipmentRequest{OrderNo: r.OrderNo, ShipmentId: shipment.ID, OrderAccessToken: r.OrderAccessToken}); err != nil {
		t.Fatal(err)
	}
	if string(d.Client.Order.GetX(ctx, o.ID).Status) != "completed" || d.Client.OrderItem.GetX(ctx, it.ID).ReceivedQuantity != it.Quantity {
		t.Fatal("authorized receive did not complete physical order")
	}
}

func TestGuestPhysicalRecoveryConcurrentConsumption(t *testing.T) {
	d, uc, in, r := guestPhysicalAccess(t)
	// Serialize database transactions while allowing requests to race at the service boundary.
	d.DB.SetMaxOpenConns(1)
	svc := NewStoreOrderService(uc, nil)
	mail := &accessRecoveryMail{ready: true}
	svc.SetRecoverySender(mail)
	ctx := context.Background()
	if _, err := svc.SendOrderAccessCode(ctx, &storefrontv1.OrderAccessCodeRequest{OrderNo: r.OrderNo, Email: in.Contact}); err != nil {
		t.Fatal(err)
	}
	request := &storefrontv1.OrderAccessRecoveryRequest{OrderNo: r.OrderNo, Email: in.Contact, Code: recoveryMailCode(t, mail)}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := svc.RecoverOrderAccess(ctx, request); results <- err }()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("same verification accepted %d times", successes)
	}
}
