//go:build integration

package testint

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"sync"
	"testing"
	"time"
)

type channelStockGate struct{ supplyport.SMSGateway }

func (channelStockGate) CheckItems(context.Context, uint64, []orderport.UpstreamStockItem) error {
	return nil
}
func TestSMSChannelRetailMySQL(t *testing.T) { smsChannelRetail(MySQL(t)) }
func TestSMSChannelRetailPG(t *testing.T)    { smsChannelRetail(PG(t)) }
func smsChannelRetail(h *Harness) {
	t := h.T
	d := h.Data
	t.Setenv("ZCARD_SMS_SALES_ENABLED", "")
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	conn := d.Client.SupplyConnection.Create().SetName("channel").SetDriver("zcard").SetBaseURL("https://example.test").SetExchangeRate(1).SetStatus("active").SetCredentials([]byte("encrypted-fixture")).SaveX(ctx)
	p := d.Client.Product.Create().SetName("SMS channel").SetSlug("channel").SetPrice(0).SetProductKind("sms_channel").SetDeliveryKind("sms_activation").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("17").SetStatus(1).SaveX(ctx)
	m := d.Client.SupplyMapping.Create().SetConnectionID(conn.ID).SetLocalProductID(p.ID).SetUpstreamProduct("17").SetPricingOverride(map[string]any{"rule": map[string]any{"mode": "fixed", "amount": 200}}).SaveX(ctx)
	q := d.Client.SMSRetailQuote.Create().SetID("channel-retail-quote").SetUserID(1).SetProductID(p.ID).SetConnectionID(conn.ID).SetConnectionIdentity(data.SMSConnectionIdentity(conn)).SetPricingRevision(data.SMSRetailPricingRevision(conn, m)).SetProductRevision(p.LockVersion).SetUpstreamQuoteID("supplier-quote").SetUpstreamProductID("17").SetAmountCents(323).SetCostCents(123).SetExpiresAt(time.Now().Add(5 * time.Minute).Unix()).SetOfferName("US / WA").SaveX(ctx)
	gen, e := id.NewGenerator(1)
	if e != nil {
		t.Fatal(e)
	}
	out := data.NewOutboxWriter(d)
	uc := &order.OrderUsecase{Data: d, Gen: gen, Inv: inventory.NewCardRepoImpl(d, nil), Outbox: out}
	uc.SetStockGate(channelStockGate{})
	in := order.CreateOrderInput{UserID: 1, IdempotencyKey: "same-purchase", SMSQuoteID: q.ID, Items: []order.OrderItemInput{{ProductID: p.ID, Quantity: 1}}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, _ = uc.CreateOrder(ctx, in) }()
	}
	close(start)
	wg.Wait()
	result, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal("same intent retry", e)
	}
	if d.Client.Order.Query().CountX(ctx) != 1 || d.Client.SMSRetailQuote.GetX(ctx, q.ID).ConsumedBy != result.OrderNo {
		t.Fatal("concurrent create duplicated order")
	}
	d.Client.WalletAccount.Create().SetUserID(1).SetAvailable(1000).SaveX(ctx)
	wr := wallet.NewWalletRepoImpl(d)
	pr := payment.NewPaymentRepoImpl(d, nil, nil, order.ProvideOrderLifecycle(uc), wallet.ProvidePortWallet(wr), nil, out, nil, nil, nil)
	d.Client.PaymentChannel.Create().SetName("Wallet").SetCode("wallet").SetDriver("wallet").SetConfig([]byte("{}")).SetEnabled(true).SaveX(ctx)
	pay := payment.NewStorePaymentService(pr, d)
	if _, e = pay.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: result.OrderNo, Channel: "wallet"}); e != nil {
		t.Fatal("wallet purchase", e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 677 || d.Client.SMSIntent.Query().CountX(ctx) != 1 {
		t.Fatal("wrong debit/intent")
	}
	// A lost response is recovered via the original order, never another debit.
	if again, e := uc.CreateOrder(ctx, in); e != nil || again.OrderNo != result.OrderNo {
		t.Fatal("paid replay", e)
	}
	intent := d.Client.SMSIntent.Query().OnlyX(ctx)
	d.Client.SMSIntent.UpdateOneID(intent.ID).SetUpstreamOrderID("supplier-order").SetChargedAmount(123).SetState("canceled").SetSettlementState("refunded").SetRefundedAmount(123).SetRefundReference("supplier-refund").SetPhase("refund").SetRetailRefundState("pending").ExecX(ctx)
	if _, e = pr.RefundSMS(ctx, intent.ID); e != nil {
		t.Fatal("retail refund", e)
	}
	if _, e = pr.RefundSMS(ctx, intent.ID); e != nil {
		t.Fatal("refund replay", e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 1000 || d.Client.RefundOrder.Query().OnlyX(ctx).Amount != 323 {
		t.Fatal("refund used supplier cost instead of retail paid amount")
	}
}
