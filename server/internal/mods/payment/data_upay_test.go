package payment

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

func upTestRequest(fn func(context.Context)) {
	srv := khttp.NewServer()
	srv.Route("/").GET("/test", func(ctx khttp.Context) error {
		fn(identity.WithClaims(ctx, &authn.Claims{Subject: 1, Realm: authn.RealmUser}))
		return ctx.String(200, "ok")
	})
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://shop.test/test", nil))
}
func upTestChannel(t *testing.T, r *PaymentRepoImpl, gateway string) *ent.PaymentChannel {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"api_url": gateway, "secret_key": "test-secret", "trade_type": "USDT-TRC20", "timeout": 600})
	ch, err := r.CreateChannel(context.Background(), "UPAY PRO", "upay", "upay", string(cfg), 0, "fixed", true, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}
func upTestResponse(w http.ResponseWriter, m map[string]any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{"trade_id": "T-" + m["order_id"].(string), "order_id": m["order_id"], "amount": m["amount"], "actual_amount": 1.5001, "token": "T-wallet", "expiration_time": time.Now().Add(5 * time.Minute).UnixMilli(), "payment_url": "https://pay.test/pay/checkout-counter/T-" + m["order_id"].(string)}})
}
func upTestCallback(ref, trade string, amount float64, status int) []byte {
	sign := fmt.Sprintf("actual_amount=1.5001&amount=%g&block_transaction_id=0&order_id=%s&status=%d&token=T-wallet&trade_id=%stest-secret", amount, ref, status, trade)
	body, _ := json.Marshal(map[string]any{"trade_id": trade, "order_id": ref, "amount": amount, "actual_amount": 1.5001, "token": "T-wallet", "block_transaction_id": "0", "status": status, "signature": fmt.Sprintf("%x", md5.Sum([]byte(sign)))})
	return body
}

func TestUpayPurchaseHTTPAndChannelLifecycle(t *testing.T) {
	d, repo, _, _, life, _ := newCallbackEnv(t)
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		if !strings.Contains(m["notify_url"].(string), "channel_id=") || !strings.HasPrefix(m["redirect_url"].(string), "https://shop.test/payment/") {
			t.Error("invalid callback/return routing")
		}
		upTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := upTestChannel(t, repo, gateway.URL)
	ctx := context.Background()
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	d.Client.PaymentChannel.UpdateOne(ch).SetFee(50).SetFeeBearer("user").SaveX(ctx)
	store := NewStorePaymentService(repo, d)
	var info *storefrontv1.CreatePaymentReply
	upTestRequest(func(ctx context.Context) {
		q, err := store.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: ch.Code})
		if err != nil {
			t.Fatal(err)
		}
		info, err = store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, QuoteKey: q.QuoteKey})
		if err != nil {
			t.Fatal(err)
		}
		q, err = store.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: ch.Code})
		if err != nil {
			t.Fatal(err)
		}
		again, err := store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, QuoteKey: q.QuoteKey})
		if err != nil || again.PaymentId != info.PaymentId || calls.Load() != 1 {
			t.Fatalf("duplicate creation %v", err)
		}
	})
	p := d.Client.Payment.GetX(ctx, info.PaymentId)
	if p.DriverSnapshot != "upay" || p.Amount != 1050 || p.ExpiresAt.After(time.Now().Add(6*time.Minute)) {
		t.Fatalf("bad snapshot %+v", p)
	}
	oldConfig := string(repo.DecryptConfig(ch))
	for _, cfg := range []string{strings.Replace(oldConfig, "test-secret", "new-secret", 1), strings.Replace(oldConfig, "USDT-TRC20", "TRX", 1)} {
		if _, err := repo.UpdateChannel(ctx, ch.ID, "", cfg, -1, "", true, -1, false, "", false, nil); err == nil || !strings.Contains(err.Error(), "CHANNEL_BUSY") {
			t.Fatalf("unsafe config change %v", err)
		}
	}
	if err := repo.DeleteChannel(ctx, ch.ID); err == nil {
		t.Fatal("enabled channel deleted")
	}
	d.Client.PaymentChannel.UpdateOne(ch).SetEnabled(false).SaveX(ctx)
	if err := repo.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	// Soft-deleted credentials still settle existing attempts.
	for _, status := range []int{1, 3} {
		rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10.5, status))
		if rec.Code != 200 || rec.Body.String() != "success" || len(life.markPaidCalls) != 0 {
			t.Fatalf("nonpaid callback %d %s", rec.Code, rec.Body.String())
		}
	}
	for i := 0; i < 2; i++ {
		rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10.5, 2))
		if rec.Code != 200 || rec.Body.String() != "success" {
			t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
		}
	}
	if len(life.markPaidCalls) != 1 || d.Client.Payment.GetX(ctx, p.ID).Status != payment.StatusSuccess {
		t.Fatal("incorrect settlement")
	}
	for _, body := range [][]byte{upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2), upTestCallback(p.GatewayOrderRef, "wrong", 10.5, 2), upTestCallback("unknown", p.ChannelOrderNo, 10.5, 2), []byte(`{"signature":"bad"}`)} {
		if rec := beServeCallback(d, repo, ch, body); rec.Code == 200 {
			t.Fatal("invalid callback acknowledged")
		}
	}
	replacement := upTestChannel(t, repo, gateway.URL)
	if replacement.Code == ch.Code {
		t.Fatal("deleted identity reused")
	}
	if rec := beServeCallback(d, repo, replacement, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10.5, 2)); rec.Code == 200 {
		t.Fatal("cross-channel callback accepted")
	}
}

func TestUpayRechargeHTTP(t *testing.T) {
	for _, target := range []rechargeorder.Target{rechargeorder.TargetBalance, rechargeorder.TargetSupply} {
		t.Run(string(target), func(t *testing.T) {
			d, repo, wallet, _, _, supplier := newCallbackEnv(t)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var m map[string]any
				_ = json.NewDecoder(r.Body).Decode(&m)
				expectedTab := "recharge"
				if target == rechargeorder.TargetSupply {
					expectedTab = "supplier"
				}
				if m["redirect_url"] != "https://shop.test/member?tab="+expectedTab {
					t.Error("wrong recharge return page")
				}
				upTestResponse(w, m)
			}))
			defer gateway.Close()
			ch := upTestChannel(t, repo, gateway.URL)
			ctx := context.Background()
			ro := d.Client.RechargeOrder.Create().SetUserID(1).SetSupplierAccountID(19).SetTarget(target).SetAmount(1000).SetGiftAmount(100).SaveX(ctx)
			var info *port.RechargePaymentInfo
			upTestRequest(func(ctx context.Context) {
				var err error
				info, err = repo.CreateRechargePayment(ctx, ro.ID, ch.Code, "", 999999)
				if err != nil {
					t.Fatal(err)
				}
			})
			p := d.Client.Payment.GetX(ctx, info.PaymentID)
			if p.Amount != 1000 {
				t.Fatal("caller amount trusted")
			}
			for i := 0; i < 2; i++ {
				rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
				if rec.Code != 200 {
					t.Fatalf("%d %s", rec.Code, rec.Body.String())
				}
			}
			if target == rechargeorder.TargetBalance {
				available, _, err := wallet.GetBalance(ctx, 1)
				if err != nil || available != 1100 {
					t.Fatalf("wrong balance %d %v", available, err)
				}
			} else if supplier.calls != 1 || supplier.amount != 1100 {
				t.Fatalf("wrong supply credit %+v", supplier)
			}
			if d.Client.RechargeOrder.GetX(ctx, ro.ID).Status != rechargeorder.StatusSuccess {
				t.Fatal("recharge not settled")
			}
		})
	}
}

func TestUpayUnknownResponseAndEarlyCallback(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprint(early), func(t *testing.T) {
			d, repo, _, _, life, _ := newCallbackEnv(t)
			d.DB.SetMaxOpenConns(1)
			var calls atomic.Int32
			var ch *ent.PaymentChannel
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var m map[string]any
				_ = json.NewDecoder(r.Body).Decode(&m)
				if early {
					ref := m["order_id"].(string)
					rec := beServeCallback(d, repo, ch, upTestCallback(ref, "T-"+ref, 10, 2))
					if rec.Code != 200 {
						t.Errorf("early callback %d %s", rec.Code, rec.Body.String())
					}
				}
				http.Error(w, "unknown response", 502)
			}))
			defer gateway.Close()
			ch = upTestChannel(t, repo, gateway.URL)
			ctx := context.Background()
			o, _ := seedPendingOrder(t, d, ch.Code, 1000)
			upTestRequest(func(ctx context.Context) {
				if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); err == nil {
					t.Fatal("expected gateway failure")
				}
			})
			p := d.Client.Payment.Query().Where(payment.ChannelID(ch.ID)).OnlyX(ctx)
			if !early && !strings.Contains(p.ReviewReason, p.GatewayOrderRef) {
				t.Fatal("unknown result not reviewable")
			}
			if !early {
				d.Client.Payment.UpdateOne(p).SetReviewReason("").SaveX(ctx)
			} // Simulate a crash before the failure flag was saved.
			restarted := NewPaymentRepoImpl(d, repo.Cipher, NewRegistry(), repo.lifecycle, repo.wallet, repo.points, repo.outbox, nil, nil, repo.supplier)
			upTestRequest(func(ctx context.Context) {
				if _, err := restarted.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); err == nil {
					t.Fatal("uncertain attempt retried")
				}
			})
			if calls.Load() != 1 {
				t.Fatal("UPAY request repeated after unknown outcome")
			}
			if !early && d.Client.Payment.GetX(ctx, p.ID).ReviewReason == "" {
				t.Fatal("crash outcome missing from review queue")
			}
			rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, "T-"+p.GatewayOrderRef, 10, 2))
			if rec.Code != 200 || len(life.markPaidCalls) != 1 || d.Client.Payment.GetX(ctx, p.ID).ReviewReason != "" {
				t.Fatalf("late recovery failed %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUpayExpiredCheckoutAndLatePayment(t *testing.T) {
	d, repo, _, _, life, _ := newCallbackEnv(t)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		upTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := upTestChannel(t, repo, gateway.URL)
	ctx := context.Background()
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	var info *port.RechargePaymentInfo
	upTestRequest(func(ctx context.Context) {
		var err error
		info, err = repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
		if err != nil {
			t.Fatal(err)
		}
	})
	p := d.Client.Payment.GetX(ctx, info.PaymentID)
	d.Client.Payment.UpdateOne(p).SetExpiresAt(time.Now().Add(-time.Second)).SaveX(ctx)
	upTestRequest(func(ctx context.Context) {
		if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); err == nil {
			t.Fatal("expired checkout reused")
		}
	})
	d.Client.Order.UpdateOne(o).SetStatus(order.StatusCanceled).SaveX(ctx)
	rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
	got := d.Client.Payment.GetX(ctx, p.ID)
	if rec.Code != 200 || got.Status != payment.StatusSuccess || got.ReviewReason == "" || len(life.markPaidCalls) != 0 {
		t.Fatal("late payment not held for review")
	}
}

func TestUpayAdminConfiguration(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	svc := NewAdminPaymentService(repo, d)
	drivers, err := svc.ListDrivers(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, driver := range drivers.Drivers {
		if driver.Code == "upay" {
			found = true
			if driver.Name != "UPAY PRO" || len(driver.Fields) != 4 || driver.Fields[2].Key != "trade_types" || !driver.Fields[2].Multiple {
				t.Fatal("incomplete driver metadata")
			}
		}
	}
	if !found {
		t.Fatal("UPAY missing")
	}
	ch, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{Name: "UPAY PRO", Code: "up", Driver: "upay", ConfigJson: `{"api_url":"https://pay.test","secret_key":"test-secret"}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ch.ConfigJson, "test-secret") {
		t.Fatal("secret exposed")
	}
	_, err = svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, ConfigJson: `{"api_url":"https://pay.test","secret_key":""}`})
	if err != nil {
		t.Fatal(err)
	}
	stored := d.Client.PaymentChannel.GetX(ctx, ch.Id)
	if !strings.Contains(string(repo.DecryptConfig(stored)), "test-secret") {
		t.Fatal("blank edit erased secret")
	}
	updated, err := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, ConfigJson: `{"trade_types":["USDT-TRC20","USDC-BSC"],"secret_key":""}`})
	if err != nil {
		t.Fatal(err)
	}
	var echo map[string]any
	if json.Unmarshal([]byte(updated.ConfigJson), &echo) != nil || len(echo["trade_types"].([]any)) != 2 || strings.Contains(updated.ConfigJson, "test-secret") {
		t.Fatal("incorrect multiselect echo")
	}
	if _, err := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, ConfigJson: `{"trade_types":[]}`}); err == nil {
		t.Fatal("empty selection saved")
	}
	if _, err := repo.CreateChannel(ctx, "bad", "bad", "upay", string(repo.DecryptConfig(stored)), 0, "fixed", true, 0, "", []map[string]any{{"code": "trx"}}); err == nil {
		t.Fatal("unsupported method overrides accepted")
	}
}

func TestUpayConcurrentClicksAndRechargeConflict(t *testing.T) {
	d, repo, wallet, _, _, _ := newCallbackEnv(t)
	d.DB.SetMaxOpenConns(1)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		upTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := upTestMultiChannel(t, repo, gateway.URL)
	ctx := context.Background()
	ro := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(1000).SaveX(ctx)
	type result struct {
		info *port.RechargePaymentInfo
		err  error
	}
	done := make(chan result, 1)
	go upTestRequest(func(ctx context.Context) {
		info, err := repo.CreateRechargePayment(ctx, ro.ID, ch.Code, "USDT-TRC20", 1000)
		done <- result{info, err}
	})
	<-started
	var busy error
	upTestRequest(func(ctx context.Context) {
		_, busy = repo.CreateRechargePayment(ctx, ro.ID, ch.Code, "USDT-TRC20", 1000)
	})
	var switchBusy error
	upTestRequest(func(ctx context.Context) {
		_, switchBusy = repo.CreateRechargePayment(ctx, ro.ID, ch.Code, "USDC-BSC", 1000)
	})
	close(release)
	first := <-done
	if switchBusy == nil || !strings.Contains(switchBusy.Error(), "IN_PROGRESS") {
		t.Fatalf("concurrent network switch bypassed lease: %v", switchBusy)
	}
	if busy == nil || !strings.Contains(busy.Error(), "IN_PROGRESS") || first.err != nil || calls.Load() != 1 {
		t.Fatalf("concurrent request: %v / %v", busy, first.err)
	}
	// Two distinct channels can both receive money, but credit the recharge only once.
	second, err := repo.CreateChannel(ctx, "UPAY second", "upay-second", "upay", string(repo.DecryptConfig(ch)), 0, "fixed", true, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var secondInfo *port.RechargePaymentInfo
	upTestRequest(func(ctx context.Context) {
		secondInfo, err = repo.CreateRechargePayment(ctx, ro.ID, second.Code, "USDC-BSC", 1000)
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []uint64{first.info.PaymentID, secondInfo.PaymentID} {
		channel := ch
		if i == 1 {
			channel = second
		}
		p := d.Client.Payment.GetX(ctx, id)
		rec := beServeCallback(d, repo, channel, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
		if rec.Code != 200 {
			t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
		}
	}
	balance, _, err := wallet.GetBalance(ctx, 1)
	if err != nil || balance != 1000 {
		t.Fatalf("double recharge credit: %d %v", balance, err)
	}
	if p := d.Client.Payment.GetX(ctx, secondInfo.PaymentID); p.Status != payment.StatusSuccess || p.ReviewReason == "" {
		t.Fatal("second receipt not recorded for review")
	}
}

func upTestMultiChannel(t *testing.T, repo *PaymentRepoImpl, gateway string) *ent.PaymentChannel {
	t.Helper()
	ch := upTestChannel(t, repo, gateway)
	cfg := strings.Replace(string(repo.DecryptConfig(ch)), `"trade_type":"USDT-TRC20"`, `"trade_types":["USDT-TRC20","USDC-BSC"]`, 1)
	ch, err := repo.UpdateChannel(context.Background(), ch.ID, "", cfg, -1, "", true, -1, false, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestUpayMultiNetworkPurchase(t *testing.T) {
	d, repo, _, _, life, _ := newCallbackEnv(t)
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		p := d.Client.Payment.Query().Where(payment.GatewayOrderRef(m["order_id"].(string))).OnlyX(context.Background())
		if pricingOf(p).Method != m["type"] {
			t.Error("selected network not sent to gateway")
		}
		upTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := upTestMultiChannel(t, repo, gateway.URL)
	ctx := context.Background()
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	store := NewStorePaymentService(repo, d)
	var ids []uint64
	upTestRequest(func(ctx context.Context) {
		for _, scene := range []string{scenePurchase, sceneMemberRecharge, sceneSupplyRecharge} {
			list, err := store.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{Scene: scene})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range list.Channels {
				if item.Code == ch.Code {
					found = true
					if len(item.Methods) != 2 || item.Methods[1].Code != "USDC-BSC" || item.Methods[1].Name != "USDC · BSC" {
						t.Fatalf("missing choices %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("channel missing")
			}
		}
		for _, method := range []string{"", "TRX", "USDT-ERC20"} {
			if _, err := store.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: method}); err == nil {
				t.Fatal("invalid quote accepted")
			}
			if _, err := store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: method}); err == nil {
				t.Fatal("invalid selection accepted")
			}
		}
		var previousQuote string
		for _, method := range []string{"USDT-TRC20", "USDC-BSC", "USDT-TRC20"} {
			q, err := store.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: method})
			if err != nil || q.Method != method {
				t.Fatalf("wrong quote %+v %v", q, err)
			}
			if previousQuote != "" {
				if _, err := store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: method, QuoteKey: previousQuote}); err == nil {
					t.Fatal("cross-network quote accepted")
				}
			}
			info, err := store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: method, QuoteKey: q.QuoteKey})
			if err != nil {
				t.Fatal(err)
			}
			p := d.Client.Payment.GetX(ctx, info.PaymentId)
			if info.Payload != "https://pay.test/pay/checkout-counter/T-"+p.GatewayOrderRef {
				t.Fatal("wrong checkout link")
			}
			ids = append(ids, info.PaymentId)
			previousQuote = q.QuoteKey
		}
	})
	if ids[0] == ids[1] || ids[0] != ids[2] || calls.Load() != 2 {
		t.Fatalf("bad attempt reuse %v / %d", ids, calls.Load())
	}
	for index, id := range ids[:2] {
		p := d.Client.Payment.GetX(ctx, id)
		for i := 0; i < 2; i++ {
			rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
			if rec.Code != 200 {
				t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
			}
		}
		// fakeLifecycle records calls only; mirror the real order state transition.
		if index == 0 {
			d.Client.Order.UpdateOne(o).SetStatus(order.StatusPaid).SaveX(ctx)
		}
	}
	if len(life.markPaidCalls) != 1 || d.Client.Payment.GetX(ctx, ids[1]).ReviewReason == "" {
		t.Fatal("cross-network double settlement")
	}
}

func TestUpayMultiNetworkRecharge(t *testing.T) {
	for _, target := range []rechargeorder.Target{rechargeorder.TargetBalance, rechargeorder.TargetSupply} {
		t.Run(string(target), func(t *testing.T) {
			d, repo, wallet, _, _, supplier := newCallbackEnv(t)
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var m map[string]any
				_ = json.NewDecoder(r.Body).Decode(&m)
				p := d.Client.Payment.Query().Where(payment.GatewayOrderRef(m["order_id"].(string))).OnlyX(context.Background())
				if pricingOf(p).Method != m["type"] {
					t.Error("wrong recharge network")
				}
				upTestResponse(w, m)
			}))
			defer gateway.Close()
			ch := upTestMultiChannel(t, repo, gateway.URL)
			ctx := context.Background()
			ro := d.Client.RechargeOrder.Create().SetUserID(1).SetSupplierAccountID(19).SetTarget(target).SetAmount(1000).SaveX(ctx)
			var ids []uint64
			upTestRequest(func(ctx context.Context) {
				if _, err := repo.CreateRechargePayment(ctx, ro.ID, ch.Code, "TRX", 1000); err == nil {
					t.Fatal("disabled network accepted")
				}
				for _, method := range []string{"USDC-BSC", "USDT-TRC20", "USDC-BSC"} {
					info, err := repo.CreateRechargePayment(ctx, ro.ID, ch.Code, method, 1000)
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, info.PaymentID)
				}
			})
			if ids[0] == ids[1] || ids[0] != ids[2] || calls.Load() != 2 {
				t.Fatalf("bad recharge reuse %v", ids)
			}
			for _, id := range ids[:2] {
				p := d.Client.Payment.GetX(ctx, id)
				rec := beServeCallback(d, repo, ch, upTestCallback(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
				if rec.Code != 200 {
					t.Fatalf("callback %s", rec.Body.String())
				}
			}
			if target == rechargeorder.TargetBalance {
				balance, _, err := wallet.GetBalance(ctx, 1)
				if err != nil || balance != 1000 {
					t.Fatalf("double credit %d %v", balance, err)
				}
			} else if supplier.calls != 1 || supplier.amount != 1000 {
				t.Fatalf("double supplier credit %+v", supplier)
			}
			if d.Client.Payment.GetX(ctx, ids[1]).ReviewReason == "" {
				t.Fatal("second receipt missing review")
			}
		})
	}
}

func TestUpayMultiNetworkUnknownCannotSwitch(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unknown", 502) }))
	defer gateway.Close()
	ch := upTestMultiChannel(t, repo, gateway.URL)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	upTestRequest(func(ctx context.Context) {
		if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "USDC-BSC"); err == nil {
			t.Fatal("expected unknown")
		}
		if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "USDT-TRC20"); err == nil || !strings.Contains(err.Error(), "RESULT_UNKNOWN") {
			t.Fatalf("unknown bypassed %v", err)
		}
	})
	if calls.Load() != 1 {
		t.Fatal("unknown request repeated on another network")
	}
}

func TestUpayLegacyPendingChoice(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		upTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := upTestChannel(t, repo, gateway.URL)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	var id uint64
	upTestRequest(func(ctx context.Context) {
		info, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		id = info.PaymentID
		p := d.Client.Payment.GetX(ctx, id)
		price := pricingOf(p)
		price.Method = ""
		d.Client.Payment.UpdateOneID(id).SetPricingSnapshot(pricingJSON(price)).SaveX(ctx)
		cfg := strings.Replace(string(repo.DecryptConfig(ch)), `"trade_type":"USDT-TRC20"`, `"trade_types":["USDT-TRC20"]`, 1)
		if _, err := repo.UpdateChannel(ctx, ch.ID, "", cfg, -1, "", true, -1, false, "", false, nil); err != nil {
			t.Fatal(err)
		}
		// Changing the legacy fallback could reassign an old attempt to another network.
		if _, err := repo.UpdateChannel(ctx, ch.ID, "", strings.Replace(cfg, `"trade_types"`, `"trade_type":"USDC-BSC","trade_types"`, 1), -1, "", true, -1, false, "", false, nil); err == nil || !strings.Contains(err.Error(), "CHANNEL_BUSY") {
			t.Fatalf("legacy attempt reassigned: %v", err)
		}
		store := NewStorePaymentService(repo, d)
		q, err := store.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: "USDT-TRC20"})
		if err != nil {
			t.Fatal(err)
		}
		again, err := store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: ch.Code, Method: "USDT-TRC20", QuoteKey: q.QuoteKey})
		if err != nil || again.PaymentId != id {
			t.Fatalf("legacy attempt lost %v", err)
		}
	})
	if calls.Load() != 1 {
		t.Fatal("legacy checkout recreated")
	}
}
