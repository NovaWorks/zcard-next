package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

func checkoutPtr[T any](v T) *T { return &v }
func checkoutUser() context.Context {
	return identity.WithClaims(context.Background(), &authn.Claims{Subject: 1, Realm: authn.RealmUser})
}

func TestCheckoutPartialConfigAndValidation(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	svc := NewAdminPaymentService(r, d)
	ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
	d.Client.PaymentChannel.UpdateOne(ch).SetFee(50).SetFeeType(paymentchannel.FeeTypePercent).SetFeeBearer(paymentchannel.FeeBearerUser).SetSort(23).SetRecommended(true).SetRecommendLabel("推荐使用").SaveX(ctx)
	for _, enabled := range []bool{false, true} {
		got, e := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.ID, Enabled: &enabled})
		if e != nil {
			t.Fatal(e)
		}
		if got.Fee != 50 || got.Sort != 23 || got.FeeBearer != "user" || !got.Recommended || got.RecommendLabel != "推荐使用" {
			t.Fatalf("partial write erased config: %+v", got)
		}
	}
	got, e := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.ID, Fee: checkoutPtr(int64(0)), Recommended: checkoutPtr(false), RecommendLabel: checkoutPtr("")})
	if e != nil {
		t.Fatal(e)
	}
	if got.Fee != 0 || got.Recommended || got.RecommendLabel != "" || !got.Enabled || got.Sort != 23 {
		t.Fatalf("explicit clear failed: %+v", got)
	}
	for _, req := range []*adminv1.UpdateChannelRequest{
		{Id: ch.ID, Fee: checkoutPtr(int64(-1))}, {Id: ch.ID, Fee: checkoutPtr(int64(10001))},
		{Id: ch.ID, FeeBearer: checkoutPtr("invalid")}, {Id: ch.ID, RecommendLabel: checkoutPtr("一二三四五六七")},
	} {
		if _, e := svc.UpdateChannel(ctx, req); e == nil {
			t.Fatalf("invalid config accepted: %+v", req)
		}
	}
}
func TestCheckoutFeeRoundingAndOverflow(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
	for _, tc := range []struct {
		base, rate, fee int64
		kind, bearer    string
	}{
		{100, 50, 1, "percent", "user"}, {50, 50, 0, "percent", "user"}, {10000, 50, 50, "percent", "user"},
		{10000, 25, 25, "fixed", "user"}, {10000, 50, 0, "percent", "merchant"}, {100, 0, 0, "fixed", "user"},
	} {
		ch.Fee = tc.rate
		ch.FeeType = paymentchannel.FeeType(tc.kind)
		ch.FeeBearer = paymentchannel.FeeBearer(tc.bearer)
		p, e := r.price(ctx, ch, tc.base, "")
		if e != nil || p.Fee != tc.fee || p.Total != tc.base+tc.fee {
			t.Fatalf("%+v => %+v %v", tc, p, e)
		}
	}
	ch.Fee = 10000
	ch.FeeBearer = paymentchannel.FeeBearerUser
	ch.FeeType = paymentchannel.FeeTypePercent
	if _, e := r.price(ctx, ch, money.MaxCents, ""); e == nil {
		t.Fatal("gross overflow accepted")
	}
}
func TestCheckoutQuoteGatewayCallbackAndOldLink(t *testing.T) {
	d, r, _, _, life, _ := newCallbackEnv(t)
	ctx := checkoutUser()
	s := NewStorePaymentService(r, d)
	ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
	d.Client.PaymentChannel.UpdateOne(ch).SetFee(50).SetFeeType(paymentchannel.FeeTypePercent).SetFeeBearer(paymentchannel.FeeBearerUser).SaveX(ctx)
	o, _ := seedPendingOrder(t, d, "epay", 10000)
	req := &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: "epay", AmountCents: 1}
	if _, e := s.QuotePayment(context.Background(), req); e == nil {
		t.Fatal("guest learned private order")
	}
	q, e := s.QuotePayment(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if q.TotalCents != 10050 || q.FeeCents != 50 {
		t.Fatalf("wrong quote %+v", q)
	}
	if _, e = s.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: "epay"}); e == nil {
		t.Fatal("fee charged without quote confirmation")
	}
	cr := &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: "epay", QuoteKey: q.QuoteKey}
	first, e := s.CreatePayment(ctx, cr)
	if e != nil {
		t.Fatal(e)
	}
	// MySQL normalizes JSON whitespace and key order on storage. Reuse is semantic.
	stored := d.Client.Payment.GetX(ctx, first.PaymentId)
	var fields map[string]any
	if err := json.Unmarshal(stored.PricingSnapshot, &fields); err != nil {
		t.Fatal(err)
	}
	normalized, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	d.Client.Payment.UpdateOneID(first.PaymentId).SetPricingSnapshot(normalized).SaveX(ctx)
	retry, e := s.CreatePayment(ctx, cr)
	if e != nil || retry.PaymentId != first.PaymentId {
		t.Fatalf("retry not reused: %v", e)
	}
	var form struct {
		Params map[string]string `json:"params"`
	}
	json.Unmarshal([]byte(first.Payload), &form)
	p := d.Client.Payment.GetX(ctx, first.PaymentId)
	if form.Params["money"] != "100.50" || form.Params["out_trade_no"] != p.GatewayOrderRef || len(p.GatewayOrderRef) > 32 {
		t.Fatalf("bad gateway amount/reference %+v", form.Params)
	}
	d.Client.PaymentChannel.UpdateOne(ch).SetFee(100).SaveX(ctx)
	if _, e = s.CreatePayment(ctx, cr); e == nil {
		t.Fatal("stale quote accepted")
	}
	q2, e := s.QuotePayment(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	cr.QuoteKey = q2.QuoteKey
	second, e := s.CreatePayment(ctx, cr)
	if e != nil {
		t.Fatal(e)
	}
	if second.PaymentId == first.PaymentId {
		t.Fatal("changed quote reused old payment")
	}
	fact := &port.CallbackFact{OrderNo: p.GatewayOrderRef, ChannelOrderNo: "old-link", Amount: 10050, Currency: "CNY", Success: true}
	if id := locatePaymentByFact(ctx, d, "epay", fact); id != p.ID {
		t.Fatal("old callback matched latest payment")
	}
	if e = r.HandleCallback(ctx, p.ID, CallbackFact{Channel: "epay", OrderNo: p.GatewayOrderRef, ChannelOrderNo: "old-link", Amount: 10000, Currency: "CNY", Success: true}); e == nil {
		t.Fatal("underpayment accepted")
	}
	cb := CallbackFact{Channel: "epay", OrderNo: p.GatewayOrderRef, ChannelOrderNo: "old-link", Amount: 10050, Currency: "CNY", Success: true}
	for i := 0; i < 2; i++ {
		if e = r.HandleCallback(ctx, p.ID, cb); e != nil {
			t.Fatal(e)
		}
	}
	if len(life.markPaidCalls) != 1 || life.markPaidCalls[0] != o.OrderNo {
		t.Fatal("settled wrong order or duplicated fulfillment")
	}
	if d.Client.Payment.GetX(ctx, second.PaymentId).Status != "pending" {
		t.Fatal("new attempt mutated by old callback")
	}
}
func TestCheckoutRechargeCreditsPrincipalOnly(t *testing.T) {
	for _, target := range []rechargeorder.Target{rechargeorder.TargetBalance, rechargeorder.TargetSupply} {
		t.Run(string(target), func(t *testing.T) {
			d, r, _, _, _, supplier := newCallbackEnv(t)
			ctx := checkoutUser()
			s := NewStorePaymentService(r, d)
			ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
			d.Client.PaymentChannel.UpdateOne(ch).SetFee(50).SetFeeBearer(paymentchannel.FeeBearerUser).SaveX(ctx)
			scene := sceneMemberRecharge
			if target == rechargeorder.TargetSupply {
				scene = sceneSupplyRecharge
			}
			q, e := s.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{Scene: scene, Channel: "epay", AmountCents: 1000})
			if e != nil {
				t.Fatal(e)
			}
			ro := d.Client.RechargeOrder.Create().SetUserID(1).SetSupplierAccountID(19).SetTarget(target).SetAmount(1000).SetGiftAmount(100).SaveX(ctx)
			info, e := r.CreateRechargePayment(port.WithQuoteKey(ctx, q.QuoteKey), ro.ID, "epay", "", 999999)
			if e != nil {
				t.Fatal(e)
			}
			p := d.Client.Payment.GetX(ctx, info.PaymentID)
			if p.Amount != 1050 || p.Fee != 50 {
				t.Fatal("trusted caller amount or omitted fee")
			}
			cb := CallbackFact{Channel: "epay", OrderNo: p.GatewayOrderRef, ChannelOrderNo: "rch-fee", Amount: 1050, Currency: "CNY", Success: true}
			for i := 0; i < 2; i++ {
				if e = r.HandleCallback(ctx, p.ID, cb); e != nil {
					t.Fatal(e)
				}
			}
			if target == rechargeorder.TargetBalance {
				if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 1100 {
					t.Fatal("fee credited to wallet")
				}
			} else if supplier.calls != 1 || supplier.amount != 1100 {
				t.Fatal("supplier credited fee or duplicate")
			}
		})
	}
}
func TestCheckoutFeeRefundSeparatelyAndIdempotently(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("refund-fees").SetUserID(1).SetStatus(order.StatusPaid).SetTotalAmount(1000).SaveX(ctx)
	d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("epay").SetAmount(1050).SetFee(50).SetStatus("success").SaveX(ctx)
	if _, e := r.RefundToWallet(ctx, o.ID, 1000, checkoutPtr(int64(0)), "principal", 1); e != nil {
		t.Fatal(e)
	}
	fee := RefundFeeInput{Amount: 50, Expected: checkoutPtr(int64(0))}
	if _, e := r.RefundToWallet(ctx, o.ID, 0, checkoutPtr(int64(1000)), "fee", 1, fee); e != nil {
		t.Fatal(e)
	}
	if _, e := r.RefundToWallet(ctx, o.ID, 0, checkoutPtr(int64(1000)), "retry", 1, fee); e == nil {
		t.Fatal("duplicate fee refund accepted")
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 1050 {
		t.Fatal("refund gross incorrect")
	}
	if d.Client.RefundOrder.Query().CountX(ctx) != 2 {
		t.Fatal("wrong refund ledger")
	}
}
func TestCheckoutBepusdtKeepsOldQuote(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := checkoutUser()
	s := NewStorePaymentService(r, d)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var m map[string]any
		json.NewDecoder(req.Body).Decode(&m)
		beTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := beTestChannel(t, r, gateway.URL)
	d.Client.PaymentChannel.UpdateOne(ch).SetFee(50).SetFeeBearer(paymentchannel.FeeBearerUser).SaveX(ctx)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	qr := &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: ch.Code}
	q, e := s.QuotePayment(ctx, qr)
	if e != nil {
		t.Fatal(e)
	}
	req := &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, QuoteKey: q.QuoteKey}
	first, e := s.CreatePayment(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	d.Client.PaymentChannel.UpdateOne(ch).SetFee(100).SaveX(ctx)
	old, e := s.QuotePayment(ctx, qr)
	if e != nil || old.TotalCents != 1050 {
		t.Fatalf("old quote changed: %+v %v", old, e)
	}
	req.QuoteKey = old.QuoteKey
	again, e := s.CreatePayment(ctx, req)
	if e != nil || again.PaymentId != first.PaymentId {
		t.Fatalf("old attempt lost: %v", e)
	}
	p := d.Client.Payment.GetX(ctx, first.PaymentId)
	rec := beServeCallback(d, r, ch, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, "10.50", 2))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "success") {
		t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
	}
}

type checkoutBlockingProvider struct {
	started chan struct{}
	release chan struct{}
	calls   int
	key     string
}

func (*checkoutBlockingProvider) Type() string                         { return "epay" }
func (*checkoutBlockingProvider) ValidateConfig(json.RawMessage) error { return nil }
func (p *checkoutBlockingProvider) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	p.calls++
	p.key = req.IdempotencyKey
	close(p.started)
	<-p.release
	return &port.RedirectInfo{Type: "redirect", Payload: json.RawMessage(`"https://pay.example/checkout"`)}, nil
}
func TestCheckoutConcurrentGatewayDispatch(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	d.DB.SetMaxOpenConns(1)
	ctx := context.Background()
	o, _ := seedPendingOrder(t, d, "epay", 1000)
	p, e := r.CreatePayment(ctx, o.ID, "epay", 1000, "")
	if e != nil {
		t.Fatal(e)
	}
	provider := &checkoutBlockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := r.dispatchPayment(ctx, p, provider, port.CreatePaymentRequest{}); done <- err }()
	<-provider.started
	if _, err := r.dispatchPayment(ctx, p, provider, port.CreatePaymentRequest{}); err == nil || !strings.Contains(err.Error(), "IN_PROGRESS") {
		t.Fatalf("concurrent dispatch: %v", err)
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := r.dispatchPayment(ctx, p, provider, port.CreatePaymentRequest{}); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || provider.key != p.GatewayOrderRef {
		t.Fatal("duplicate gateway call or unstable idempotency key")
	}
}
func TestCheckoutCrossCurrencyUsesGrossSnapshot(t *testing.T) {
	d, r, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	r.currency = precisionCurrency{rate: "0.14", precision: 0}
	cfg, e := r.Cipher.Seal([]byte(`{"target_currency":"USD"}`), []byte("payment_channel:fee-stripe"))
	if e != nil {
		t.Fatal(e)
	}
	ch := d.Client.PaymentChannel.Create().SetName("Stripe").SetCode("fee-stripe").SetDriver("stripe").SetConfig(cfg).SetFee(50).SetFeeType(paymentchannel.FeeTypePercent).SetFeeBearer(paymentchannel.FeeBearerUser).SaveX(ctx)
	p, e := r.price(ctx, ch, 10000, "")
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 10050 || p.Charge.Units != 1407 || p.Charge.Currency != "USD" || p.Charge.Precision != 2 {
		t.Fatalf("wrong gross conversion %+v", p)
	}
	o, _ := seedPendingOrder(t, d, ch.Code, 10000)
	q := pricingQuote(p, ch.Code, ch.ID, o.OrderNo, 0)
	pay, e := r.CreatePayment(port.WithQuoteKey(ctx, q.QuoteKey), o.ID, ch.Code, 1, "")
	if e != nil {
		t.Fatal(e)
	}
	r.currency = precisionCurrency{rate: "0.5", precision: 6}
	if e = r.HandleCallback(ctx, pay.ID, CallbackFact{Channel: ch.Code, OrderNo: pay.GatewayOrderRef, ChannelOrderNo: "usd-trade", Amount: 1407, Currency: "USD", Success: true}); e != nil {
		t.Fatal(e)
	}
	if got := d.Client.Payment.GetX(ctx, pay.ID); got.ChargedAmount != 10050 || got.Fee != 50 {
		t.Fatal("callback used new exchange rate")
	}
}
