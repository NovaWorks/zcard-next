package order

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"testing"
	"time"
)

func channelOrderFixture(t *testing.T) (*data.Data, *OrderUsecase, *ent.SMSRetailQuote, CreateOrderInput) {
	t.Helper()
	t.Setenv("ZCARD_SMS_SALES_ENABLED", "")
	d, uc, pid := newGateEnv(t)
	ctx := context.Background()
	p := d.Client.Product.UpdateOneID(pid).SetProductKind("sms_channel").SetDeliveryKind("sms_activation").SetPrice(0).SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetID(9).SetName("channel").SetDriver("zcard").SetBaseURL("https://example.test").SetStatus("active").SetExchangeRate(1).SetCredentials([]byte("fixture")).SaveX(ctx)
	m := d.Client.SupplyMapping.Create().SetConnectionID(conn.ID).SetUpstreamProduct(p.UpstreamProductCode).SetLocalProductID(pid).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": "fixed", "amount": 200}}).SaveX(ctx)
	q := d.Client.SMSRetailQuote.Create().SetID("retail-fixed-quote").SetUserID(1).SetProductID(pid).SetConnectionID(conn.ID).SetConnectionIdentity(data.SMSConnectionIdentity(conn)).SetPricingRevision(data.SMSRetailPricingRevision(conn, m)).SetProductRevision(p.LockVersion).SetUpstreamQuoteID("upstream-quote-opaque").SetUpstreamProductID(p.UpstreamProductCode).SetCostCents(123).SetAmountCents(323).SetExpiresAt(time.Now().Add(time.Minute).Unix()).SetOfferName("国家 / 服务").SetSelection(map[string]string{"offer_name": "国家 / 服务"}).SaveX(ctx)
	uc.SetStockGate(&smsGate{})
	return d, uc, q, CreateOrderInput{UserID: 1, IdempotencyKey: "stable-intent", SMSQuoteID: q.ID, Items: []OrderItemInput{{ProductID: pid, Quantity: 1}}}
}
func TestChannelOrderFreezesRetailAndSupplierQuote(t *testing.T) {
	d, uc, q, in := channelOrderFixture(t)
	ctx := context.Background()
	result, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if result.TotalCents != 323 || result.ExpiresAt.Unix() != q.ExpiresAt || d.Client.SMSRetailQuote.GetX(ctx, q.ID).ConsumedBy != result.OrderNo {
		t.Fatal("retail amount/expiry/quote not frozen")
	}
	if d.Client.SMSIntent.Query().CountX(ctx) != 0 {
		t.Fatal("supplier called before payment")
	}
	if e = uc.MarkPaid(ctx, result.OrderNo); e != nil {
		t.Fatal(e)
	}
	if e = uc.MarkPaid(ctx, result.OrderNo); e != nil {
		t.Fatal(e)
	}
	row := d.Client.SMSIntent.Query().OnlyX(ctx)
	var req supplyport.SMSPurchase
	if json.Unmarshal([]byte(row.RequestJSON), &req) != nil || req.SMSQuoteID != "upstream-quote-opaque" || req.RequiredCapability != supplyport.SMSProductPurchase || req.MaxSupplyAmountCents != 123 {
		t.Fatal("wrong immutable supplier request", row.RequestJSON)
	}
	d.Client.SMSRetailQuote.UpdateOneID(q.ID).SetExpiresAt(1).ExecX(ctx)
	d.Client.Product.UpdateOneID(in.Items[0].ProductID).SetStatus(0).ExecX(ctx)
	t.Setenv("ZCARD_SMS_SALES_ENABLED", "false")
	again, e := uc.CreateOrder(ctx, in)
	if e != nil || again.OrderNo != result.OrderNo || d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatal("lost result created second order", e)
	}
	changed := in
	changed.SMSQuoteID = "different-quote"
	if _, e = uc.CreateOrder(ctx, changed); e == nil {
		t.Fatal("changed intent accepted")
	}
}
func TestChannelQuoteOwnershipExpiryAndConfiguration(t *testing.T) {
	for _, kind := range []string{"missing", "user", "expired", "consumed", "markup", "source", "plain_checkout", "second_intent"} {
		t.Run(kind, func(t *testing.T) {
			d, uc, q, in := channelOrderFixture(t)
			ctx := context.Background()
			switch kind {
			case "missing":
				in.SMSQuoteID = "missing"
			case "user":
				in.UserID = 2
			case "expired":
				d.Client.SMSRetailQuote.UpdateOneID(q.ID).SetExpiresAt(1).ExecX(ctx)
			case "consumed":
				d.Client.SMSRetailQuote.UpdateOneID(q.ID).SetConsumedBy("other").ExecX(ctx)
			case "markup":
				d.Client.SupplyConnection.UpdateOneID(9).SetPriceMarkupAmount(1).ExecX(ctx)
			case "source":
				d.Client.SupplyConnection.UpdateOneID(9).SetCredentials([]byte("changed")).ExecX(ctx)
			case "plain_checkout":
				in.SMSQuoteID = ""
				in.QueryPassword = "test1234"
			case "second_intent":
				if _, e := uc.CreateOrder(ctx, in); e != nil {
					t.Fatal(e)
				}
				in.IdempotencyKey = "different-request"
			}
			before := d.Client.Order.Query().CountX(ctx)
			if _, e := uc.CreateOrder(ctx, in); e == nil {
				t.Fatal("invalid quote accepted")
			}
			if d.Client.Order.Query().CountX(ctx) != before || d.Client.SMSIntent.Query().CountX(ctx) != 0 {
				t.Fatal("invalid quote created obligation")
			}
		})
	}
}
