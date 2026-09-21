package payment

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func beTestChannel(t *testing.T, r *PaymentRepoImpl, gateway string) *ent.PaymentChannel {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"api_url": gateway, "api_token": "contract-test-token", "timeout": 600, "trade_type": "usdt.trc20"})
	ch, err := r.CreateChannel(context.Background(), "BEpusdt", "be", "bepusdt", string(cfg), 0, "fixed", true, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func beTestResponse(w http.ResponseWriter, m map[string]any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{"order_id": m["order_id"], "trade_id": "T-" + m["order_id"].(string), "amount": m["amount"], "fiat": m["fiat"], "trade_type": m["trade_type"], "status": 1, "expiration_time": 590, "payment_url": "https://pay.example/pay/" + m["order_id"].(string)}})
}
func beTestCallbackBody(ref, trade string, amount any, status int) []byte {
	m := map[string]any{"trade_id": trade, "order_id": ref, "amount": amount, "actual_amount": "1.4231", "token": "T-address", "block_transaction_id": "block-123", "status": status}
	// Upstream native notification marshals, decodes to float64, then signs.
	raw, _ := json.Marshal(m)
	_ = json.Unmarshal(raw, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := []string{}
	for _, k := range keys {
		if m[k] != nil && m[k] != "" {
			pairs = append(pairs, fmt.Sprint(k, "=", m[k]))
		}
	}
	m["signature"] = fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(pairs, "&")+"contract-test-token")))
	raw, _ = json.Marshal(m)
	return raw
}
func beServeCallback(d *data.Data, r *PaymentRepoImpl, ch *ent.PaymentChannel, body []byte) *httptest.ResponseRecorder {
	srv := khttp.NewServer()
	RegisterPaymentCallback(srv, r, d)
	req := httptest.NewRequest("POST", fmt.Sprintf("/payments/callback/%s?channel_id=%d", ch.Code, ch.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestBepusdtAttemptReuseAndConcurrentClicks(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, _, _ := newCallbackEnv(t)
	d.DB.SetMaxOpenConns(1)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		calls.Add(1)
		close(started)
		<-release
		beTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := beTestChannel(t, repo, gateway.URL)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	_, _ = d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().Add(15 * time.Minute)).Save(ctx)
	type result struct {
		info *port.RechargePaymentInfo
		err  error
	}
	done := make(chan result, 1)
	go func() { info, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); done <- result{info, err} }()
	<-started
	_, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
	close(release)
	if err == nil || !strings.Contains(err.Error(), "IN_PROGRESS") {
		t.Fatalf("concurrent click: %v", err)
	}
	first := <-done
	if first.err != nil {
		t.Fatal(first.err)
	}
	again, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
	if err != nil || again.PaymentID != first.info.PaymentID || again.Payload != first.info.Payload || calls.Load() != 1 {
		t.Fatalf("retry %+v err=%v calls=%d", again, err, calls.Load())
	}
	p, _ := repo.GetPayment(ctx, again.PaymentID)
	if p.GatewayOrderRef == "" || p.ChannelOrderNo != "T-"+p.GatewayOrderRef || strings.Contains(string(p.GatewayContext), "contract-test-token") {
		t.Fatal("attempt not safely persisted")
	}
	// Deadline never refreshes on cache reuse and old attempts are never replaced.
	_, _ = d.Client.Payment.UpdateOneID(p.ID).SetExpiresAt(time.Now().Add(-time.Second)).Save(ctx)
	if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); err == nil {
		t.Fatal("expired attempt reused")
	}
	count, _ := d.Client.Payment.Query().Where(payment.ChannelID(ch.ID)).Count(ctx)
	if count != 1 {
		t.Fatalf("attempts=%d", count)
	}
}

func TestBepusdtLostResponseRecoveryAndConfigGuard(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, _, _ := newCallbackEnv(t)
	refs := []string{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		refs = append(refs, m["order_id"].(string))
		if len(refs) == 1 {
			http.Error(w, "lost", 502)
			return
		}
		beTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := beTestChannel(t, repo, gateway.URL)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	_, _ = d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().Add(15 * time.Minute)).Save(ctx)
	if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); err == nil {
		t.Fatal("expected upstream error")
	}
	old, _ := d.Client.Payment.Query().Where(payment.ChannelID(ch.ID)).Only(ctx)
	raw := string(repo.DecryptConfig(ch))
	changed := strings.Replace(raw, "contract-test-token", "new-token", 1)
	if _, err := repo.UpdateChannel(ctx, ch.ID, "", changed, -1, "", true, -1, false, "", false, nil); err == nil || !strings.Contains(err.Error(), "CHANNEL_BUSY") {
		t.Fatalf("credential change: %v", err)
	}
	if _, err := repo.UpdateChannel(ctx, ch.ID, "renamed", raw, -1, "", true, -1, false, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteChannel(ctx, ch.ID); err == nil {
		t.Fatal("deleted callback credentials")
	}
	// A fresh repo instance simulates restart. The failed HTTP request is not an in-memory lock.
	restarted := NewPaymentRepoImpl(d, repo.Cipher, NewRegistry(), repo.lifecycle, repo.wallet, repo.points, repo.outbox, nil, nil, repo.supplier)
	info, err := restarted.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.PaymentID != old.ID || len(refs) != 2 || refs[0] != refs[1] {
		t.Fatalf("lost-response recovery %#v", refs)
	}
	now, _ := repo.GetPayment(ctx, old.ID)
	if now.ExpiresAt.After(old.ExpiresAt) {
		t.Fatal("deadline extended")
	}
}

func TestBepusdtCallbackBeforeCreateResponse(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, life, _ := newCallbackEnv(t)
	d.DB.SetMaxOpenConns(1)
	var ch *ent.PaymentChannel
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		ref := m["order_id"].(string)
		rec := beServeCallback(d, repo, ch, beTestCallbackBody(ref, "T-"+ref, 10, 2))
		if rec.Code != 200 {
			t.Errorf("early callback: %d %s", rec.Code, rec.Body.String())
		}
		beTestResponse(w, m)
	}))
	defer gateway.Close()
	ch = beTestChannel(t, repo, gateway.URL)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	_, _ = d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().Add(15 * time.Minute)).Save(ctx)
	info, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := repo.GetPayment(ctx, info.PaymentID)
	if p.Status != payment.StatusSuccess || len(life.markPaidCalls) != 1 {
		t.Fatal("callback lost or overwritten")
	}
	rec := beServeCallback(d, repo, ch, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
	if rec.Code != 200 || rec.Body.String() != "success" || len(life.markPaidCalls) != 1 {
		t.Fatal("duplicate callback not idempotent")
	}
	// Already-paid callbacks still check amount and trade identity.
	for _, body := range [][]byte{beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 9, 2), beTestCallbackBody(p.GatewayOrderRef, "wrong-trade", 10, 2), beTestCallbackBody("unknown", p.ChannelOrderNo, 10, 2)} {
		if rec := beServeCallback(d, repo, ch, body); rec.Code == 200 {
			t.Fatal("invalid callback acknowledged")
		}
	}
}

func TestBepusdtRechargeCallbacks(t *testing.T) {
	for _, target := range []rechargeorder.Target{rechargeorder.TargetBalance, rechargeorder.TargetSupply} {
		t.Run(string(target), func(t *testing.T) {
			ctx := context.Background()
			d, repo, wallet, writer, _, supplier := newCallbackEnv(t)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var m map[string]any
				_ = json.NewDecoder(r.Body).Decode(&m)
				beTestResponse(w, m)
			}))
			defer gateway.Close()
			ch := beTestChannel(t, repo, gateway.URL)
			builder := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(1000).SetTarget(target).SetStatus(rechargeorder.StatusPending)
			if target == rechargeorder.TargetSupply {
				builder.SetSupplierAccountID(19)
			} else {
				builder.SetGiftAmount(100)
			}
			ro, err := builder.Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			info, err := repo.CreateRechargePayment(ctx, ro.ID, ch.Code, "", 1)
			if err != nil {
				t.Fatal(err)
			} // client-supplied amount is not authoritative
			p, _ := repo.GetPayment(ctx, info.PaymentID)
			if p.Amount != 1000 {
				t.Fatal("used untrusted recharge amount")
			}
			for _, status := range []int{1, 3, 4, 5, 6} {
				rec := beServeCallback(d, repo, ch, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 10, status))
				if rec.Code != 200 || rec.Body.String() != "success" {
					t.Fatalf("status %d: %d", status, rec.Code)
				}
			}
			pending, _ := repo.GetPayment(ctx, p.ID)
			if pending.Status != payment.StatusPending {
				t.Fatal("nonpaid callback settled")
			}
			other, err := repo.CreateChannel(ctx, "other", "other", "bepusdt", string(repo.DecryptConfig(ch)), 0, "fixed", true, 0, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if rec := beServeCallback(d, repo, other, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2)); rec.Code == 200 {
				t.Fatal("cross-channel callback accepted")
			}
			if rec := beServeCallback(d, repo, ch, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 9.99, 2)); rec.Code == 200 {
				t.Fatal("underpayment acknowledged")
			}
			for i := 0; i < 2; i++ {
				rec := beServeCallback(d, repo, ch, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
				if rec.Code != 200 || rec.Body.String() != "success" {
					t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
				}
			}
			duplicate, err := d.Client.Payment.Create().SetRechargeOrderID(ro.ID).SetChannel(other.Code).SetChannelID(other.ID).SetDriverSnapshot("bepusdt").SetGatewayOrderRef("BE-second-recharge").SetAmount(1000).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rec := beServeCallback(d, repo, other, beTestCallbackBody(duplicate.GatewayOrderRef, "T-second-recharge", 10, 2))
			if rec.Code != 200 {
				t.Fatalf("second paid recharge %d %s", rec.Code, rec.Body.String())
			}
			duplicate, _ = repo.GetPayment(ctx, duplicate.ID)
			if duplicate.Status != payment.StatusSuccess || duplicate.ReviewReason == "" {
				t.Fatal("second receipt must be retained for review")
			}
			if !writer.has("recharge.succeeded") {
				t.Fatal("no recharge event")
			}
			if target == rechargeorder.TargetSupply {
				if supplier.calls != 1 || supplier.amount != 1000 || supplier.accountID != 19 {
					t.Fatalf("supplier %+v", supplier)
				}
			} else {
				balance, _, err := wallet.GetBalance(ctx, 1)
				if err != nil || balance != 1100 {
					t.Fatalf("balance %v err %v", balance, err)
				}
			}
		})
	}
}

func TestBepusdtLatePaymentAndLeaseRecovery(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, life, _ := newCallbackEnv(t)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		beTestResponse(w, m)
	}))
	defer gateway.Close()
	ch := beTestChannel(t, repo, gateway.URL)
	o, _ := seedPendingOrder(t, d, ch.Code, 1000)
	// Simulate a process dying after persisting a lease, before recording the response.
	state := bepusdtAttempt{Subject: "test", ReturnURL: "https://shop.example/return", NotifyURL: "https://shop.example/notify", Lease: "crashed", LeaseUntil: time.Now().Add(-time.Second)}
	p, err := d.Client.Payment.Create().SetOrderID(o.ID).SetChannelID(ch.ID).SetChannel(ch.Code).SetDriverSnapshot("bepusdt").SetAmount(1000).SetGatewayOrderRef("BE-crash").SetExpiresAt(time.Now().Add(600 * time.Second)).SetGatewayContext(encodeBepusdtAttempt(state)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
	if err != nil || info.PaymentID != p.ID {
		t.Fatalf("lease recovery: %+v %v", info, err)
	}
	if _, err := repo.UpdateChannel(ctx, ch.ID, "", "", -1, "", false, -1, false, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Client.Order.UpdateOneID(o.ID).SetStatus("expired").Save(ctx)
	rec := beServeCallback(d, repo, ch, beTestCallbackBody(p.GatewayOrderRef, "T-"+p.GatewayOrderRef, 10, 2))
	if rec.Code != 200 {
		t.Fatalf("late callback %d %s", rec.Code, rec.Body.String())
	}
	p, _ = repo.GetPayment(ctx, p.ID)
	if p.Status != "success" || p.ReviewReason == "" || len(life.markPaidCalls) != 0 {
		t.Fatal("late payment must enter review without fulfillment")
	}
}

func TestBepusdtDatabaseConcurrency(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_BE_FLOW_" + strings.ToUpper(driver) + "_DSN")
			if source == "" {
				t.Skip("requires disposable database")
			}
			ctx := context.Background()
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if err := d.Client.Schema.Create(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Client.User.Create().SetUsername("be-concurrency").Save(ctx); err != nil {
				t.Fatal(err)
			}
			box, err := crypto.NewBox(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			life := &fakeLifecycle{}
			repo := NewPaymentRepoImpl(d, box, NewRegistry(), life, nil, nil, &captureWriter{}, nil, nil, nil)
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var m map[string]any
				_ = json.NewDecoder(r.Body).Decode(&m)
				if calls.Add(1) == 1 {
					close(started)
				}
				<-release
				beTestResponse(w, m)
			}))
			defer gateway.Close()
			ch := beTestChannel(t, repo, gateway.URL)
			o, _ := seedPendingOrder(t, d, ch.Code, 1000)
			result := make(chan error, 1)
			go func() { _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); result <- err }()
			<-started
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, "")
					if err == nil || !strings.Contains(err.Error(), "IN_PROGRESS") {
						t.Errorf("concurrent click %v", err)
					}
				}()
			}
			wg.Wait()
			close(release)
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("requests=%d", calls.Load())
			}
			p, err := d.Client.Payment.Query().Where(payment.ChannelID(ch.ID)).Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					rec := beServeCallback(d, repo, ch, beTestCallbackBody(p.GatewayOrderRef, p.ChannelOrderNo, 10, 2))
					if rec.Code != 200 {
						t.Errorf("concurrent callback %d %s", rec.Code, rec.Body.String())
					}
				}()
			}
			wg.Wait()
			if len(life.markPaidCalls) != 1 {
				t.Fatalf("settlements=%d", len(life.markPaidCalls))
			}
		})
	}
}
