package order

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"testing"
)

type smsGate struct {
	fakeStockGate
	q supplyport.SMSQuote
}

func (g *smsGate) PrepareSMS(context.Context, uint64, string) (supplyport.SMSQuote, error) {
	return g.q, nil
}
func (g *smsGate) OpenSMS(context.Context, uint64, string) (supplyport.SMSClient, error) {
	return nil, fmt.Errorf("not used")
}
func TestSMSPaidIntentAtomicAndInputRestrictions(t *testing.T) {
	t.Setenv("ZCARD_SMS_SALES_ENABLED", "true")
	d, uc, pid := newGateEnv(t)
	ctx := context.Background()
	p := d.Client.Product.UpdateOneID(pid).SetDeliveryKind("sms_activation").SetFactoryPrice(123).SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetID(9).SetName("fixture").SetDriver("zcard").SetBaseURL("https://example.test").SetCredentials([]byte("encrypted-fixture")).SaveX(ctx)
	gate := &smsGate{q: supplyport.SMSQuote{ProductID: p.UpstreamProductCode, ConnectionID: 9, Identity: data.SMSConnectionIdentity(conn), Amount: 123}}
	uc.SetStockGate(gate)
	valid := CreateOrderInput{UserID: 1, Contact: "test@example.test", QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: pid, Quantity: 1}}}
	for _, kind := range []string{"guest", "qty", "sku", "coupon", "points", "mixed", "off"} {
		t.Run(kind, func(t *testing.T) {
			in := valid
			in.Items = append([]OrderItemInput(nil), valid.Items...)
			switch kind {
			case "guest":
				in.UserID = 0
			case "qty":
				in.Items[0].Quantity = 2
			case "sku":
				in.Items[0].SkuID = 1
			case "coupon":
				in.CouponCode = "gift"
			case "points":
				in.UsePoints = true
			case "mixed":
				in.Items = append(in.Items, in.Items[0])
			case "off":
				t.Setenv("ZCARD_SMS_SALES_ENABLED", "false")
			}
			if _, e := uc.CreateOrder(ctx, in); e == nil {
				t.Fatal("invalid SMS order allowed")
			}
		})
	}
	result, e := uc.CreateOrder(ctx, valid)
	if e != nil {
		t.Fatal(e)
	}
	if d.Client.SMSIntent.Query().CountX(ctx) != 0 {
		t.Fatal("purchased before payment")
	}
	// Force a rollback after MarkPaid: neither the status nor its task survives.
	e = data.Tx(ctx, d, func(tx context.Context) error {
		if e := uc.MarkPaid(tx, result.OrderNo); e != nil {
			return e
		}
		return fmt.Errorf("crash before commit")
	})
	if e == nil || d.Client.SMSIntent.Query().CountX(ctx) != 0 {
		t.Fatal("task escaped transaction")
	}
	if e = uc.MarkPaid(ctx, result.OrderNo); e != nil {
		t.Fatal(e)
	}
	if e = uc.MarkPaid(ctx, result.OrderNo); e != nil {
		t.Fatal(e)
	}
	task := d.Client.SMSIntent.Query().OnlyX(ctx)
	var req supplyport.SMSPurchase
	if json.Unmarshal([]byte(task.RequestJSON), &req) != nil || req.MaxSupplyAmountCents != 123 || req.Quantity != 1 {
		t.Fatal("intent not frozen")
	}
	if d.Client.OrderItem.Query().OnlyX(ctx).DeliveryKind != "sms_activation" {
		t.Fatal("order item type lost")
	}
}
