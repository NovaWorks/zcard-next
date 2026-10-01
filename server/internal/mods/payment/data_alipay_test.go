package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func alipayServiceConfig(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]string{"app_id": "2021001", "private_key": base64.StdEncoding.EncodeToString(der), "alipay_public_key": base64.StdEncoding.EncodeToString(pub)})
	return string(cfg), priv
}

func TestAlipayConfigSaveAndRedaction(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewAdminPaymentService(repo, d)
	cfg, _ := alipayServiceConfig(t)
	ch, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{Name: "支付宝", Code: "alipay-test", Driver: "alipay", ConfigJson: cfg, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var echo map[string]string
	_ = json.Unmarshal([]byte(ch.ConfigJson), &echo)
	if echo["private_key"] != "****" || echo["alipay_public_key"] != "****" {
		t.Fatal("saved keys are not masked")
	}
	patch := `{"private_key":"","alipay_public_key":"****","app_id":"2021002"}`
	if _, err := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, ConfigJson: patch}); err != nil {
		t.Fatal(err)
	}
	stored, err := d.Client.PaymentChannel.Get(ctx, ch.Id)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]string
	_ = json.Unmarshal([]byte(cfg), &before)
	_ = json.Unmarshal(repo.DecryptConfig(stored), &after)
	if after["private_key"] != before["private_key"] || after["alipay_public_key"] != before["alipay_public_key"] || after["app_id"] != "2021002" {
		t.Fatal("masked or empty keys replaced original keys")
	}
	patch = `{"private_key":"not-a-key"}`
	if _, err := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, ConfigJson: patch}); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	stored, _ = d.Client.PaymentChannel.Get(ctx, ch.Id)
	_ = json.Unmarshal(repo.DecryptConfig(stored), &after)
	if after["private_key"] != before["private_key"] {
		t.Fatal("failed validation changed stored private key")
	}
}

func signAlipayHTTPForm(t *testing.T, form url.Values, priv *rsa.PrivateKey) {
	t.Helper()
	keys := []string{}
	for k := range form {
		if k != "sign" && k != "sign_type" && form.Get(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := []string{}
	for _, k := range keys {
		pairs = append(pairs, k+"="+form.Get(k))
	}
	digest := sha256.Sum256([]byte(strings.Join(pairs, "&")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	form.Set("sign", base64.StdEncoding.EncodeToString(sig))
}

func TestAlipayHTTPCallbackACKAndIdempotency(t *testing.T) {
	for _, status := range []string{"TRADE_SUCCESS", "TRADE_FINISHED", "TRADE_CLOSED", "WAIT_BUYER_PAY"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			d, repo, _, _, lifecycle, _ := newCallbackEnv(t)
			cfg, priv := alipayServiceConfig(t)
			ch, err := repo.CreateChannel(ctx, "支付宝", "alipay-http", "alipay", cfg, 0, "fixed", true, 0, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, p := seedPendingOrder(t, d, "alipay-http", 1000)
			p = d.Client.Payment.UpdateOneID(p.ID).SetChannelID(ch.ID).SetDriverSnapshot("alipay").SetGatewayOrderRef("ZP-alipay-http").SaveX(ctx)
			srv := khttp.NewServer()
			RegisterPaymentCallback(srv, repo, d)
			form := url.Values{"app_id": {"2021001"}, "sign_type": {"RSA2"}, "out_trade_no": {"ZP-alipay-http"}, "trade_no": {"T-http"}, "total_amount": {"10.00"}, "trade_status": {status}}
			send := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", fmt.Sprintf("/payments/callback/alipay-http?channel_id=%d", ch.ID), strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				return rec
			}
			form.Set("app_id", "other-app")
			signAlipayHTTPForm(t, form, priv)
			if rec := send(); rec.Code != 401 {
				t.Fatal("wrong APPID accepted")
			}
			form.Set("app_id", "2021001")
			signAlipayHTTPForm(t, form, priv)
			for i := 0; i < 2; i++ {
				if rec := send(); rec.Code != 200 || rec.Body.String() != "success" {
					t.Fatalf("wrong ACK: %d %s", rec.Code, rec.Body.String())
				}
			}
			paid := status == "TRADE_SUCCESS" || status == "TRADE_FINISHED"
			if (d.Client.Payment.GetX(ctx, p.ID).Status == "success") != paid {
				t.Fatal("incorrect payment state")
			}
			count := 0
			if paid {
				count = 1
			}
			if len(lifecycle.markPaidCalls) != count {
				t.Fatal("duplicate or unpaid notification settled order")
			}
		})
	}
}

type alipayOrderQuerySpy struct {
	*adapter.AlipayAdapter
	ref     string
	byOrder bool
	fact    *port.CallbackFact
}

func (a *alipayOrderQuerySpy) QueryPayment(ctx context.Context, ref string, cfg json.RawMessage) (*port.CallbackFact, error) {
	a.ref = ref
	return a.fact, nil
}
func (a *alipayOrderQuerySpy) QueryPaymentByOrderNo(ctx context.Context, ref string, cfg json.RawMessage) (*port.CallbackFact, error) {
	a.ref = ref
	a.byOrder = true
	return a.fact, nil
}

func TestAlipayCaptureWithoutNotification(t *testing.T) {
	for _, mode := range []string{"new_order", "legacy_order", "legacy_trade", "new_recharge", "legacy_recharge"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			d, repo, walletRepo, _, lifecycle, _ := newCallbackEnv(t)
			ch, err := repo.CreateChannel(ctx, "支付宝", "alipay-capture", "alipay", `{}`, 0, "fixed", true, 0, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			o, p := seedPendingOrder(t, d, "alipay-capture", 1000)
			update := d.Client.Payment.UpdateOneID(p.ID).SetChannelID(ch.ID).SetDriverSnapshot("alipay")
			ref := o.OrderNo
			if strings.Contains(mode, "recharge") {
				recharge := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(1000).SetStatus("pending").SaveX(ctx)
				update = update.ClearOrderID().SetRechargeOrderID(recharge.ID)
				ref = fmt.Sprintf("RCH%d", recharge.ID)
			}
			if strings.HasPrefix(mode, "new_") {
				ref = "ZP-capture"
				update = update.SetGatewayOrderRef(ref)
			}
			expected := ref
			byOrder := true
			if mode == "legacy_trade" {
				update = update.SetChannelOrderNo("T-existing")
				expected = "T-existing"
				byOrder = false
			}
			p = update.SaveX(ctx)
			spy := &alipayOrderQuerySpy{AlipayAdapter: adapter.NewAlipay(), fact: &port.CallbackFact{Provider: "alipay", OrderNo: ref, ChannelOrderNo: "T-existing", Amount: 1000, Currency: "CNY", Success: true}}
			repo.reg.Register(spy)
			svc := NewAdminPaymentService(repo, d)
			if _, err := svc.CapturePayment(ctx, &adminv1.CapturePaymentRequest{Id: p.ID}); err != nil {
				t.Fatal(err)
			}
			if spy.ref != expected || spy.byOrder != byOrder {
				t.Fatalf("incorrect lookup: %s byOrder=%v", spy.ref, spy.byOrder)
			}
			if d.Client.Payment.GetX(ctx, p.ID).Status != "success" {
				t.Fatal("capture did not settle payment")
			}
			if strings.Contains(mode, "recharge") {
				balance, _, err := walletRepo.GetBalance(ctx, 1)
				if err != nil || balance != 1000 {
					t.Fatalf("recharge not credited: %d %v", balance, err)
				}
			}
			if !strings.Contains(mode, "recharge") && len(lifecycle.markPaidCalls) != 1 {
				t.Fatal("order not settled")
			}
		})
	}
}
