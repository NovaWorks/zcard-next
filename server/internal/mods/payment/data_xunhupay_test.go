package payment

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

type hpjTestGateway struct {
	*adapter.XunhupayAdapter
	requests []port.CreatePaymentRequest
}

func (p *hpjTestGateway) CreatePayment(_ context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	p.requests = append(p.requests, req)
	return &port.RedirectInfo{Type: "qrcode", Deadline: time.Now().Add(5 * time.Minute), Payload: json.RawMessage(`{"code_url":"https://pay.example/qr.png","mobile_url":"https://pay.example/mobile"}`)}, nil
}

func hpjTestSign(form url.Values) {
	keys := make([]string, 0, len(form))
	for k := range form {
		if k != "hash" && form.Get(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+form.Get(k))
	}
	form.Set("hash", fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "&")+"test-secret"))))
}

func TestXunhupayRechargeHTTP(t *testing.T) {
	for _, target := range []rechargeorder.Target{rechargeorder.TargetBalance, rechargeorder.TargetSupply} {
		t.Run(string(target), func(t *testing.T) {
			d, repo, wallet, _, _, supplier := newCallbackEnv(t)
			ctx := checkoutUser()
			gateway := &hpjTestGateway{XunhupayAdapter: adapter.NewXunhupay()}
			repo.reg.Register(gateway)
			ch, err := repo.CreateChannel(ctx, "虎皮椒", "hpj", "xunhupay", `{"appid":"test-app","appsecret":"test-secret"}`, 50, "fixed", true, 0, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			d.Client.PaymentChannel.UpdateOne(ch).SetFeeBearer("user").SaveX(ctx)
			scene := sceneMemberRecharge
			if target == rechargeorder.TargetSupply {
				scene = sceneSupplyRecharge
			}
			quote, err := NewStorePaymentService(repo, d).QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{Scene: scene, Channel: ch.Code, AmountCents: 1000})
			if err != nil {
				t.Fatal(err)
			}
			ro := d.Client.RechargeOrder.Create().SetUserID(1).SetSupplierAccountID(19).SetTarget(target).SetAmount(1000).SetGiftAmount(100).SaveX(ctx)
			info, err := repo.CreateRechargePayment(port.WithQuoteKey(ctx, quote.QuoteKey), ro.ID, ch.Code, "", 999999)
			if err != nil {
				t.Fatal(err)
			}
			p := d.Client.Payment.GetX(ctx, info.PaymentID)
			if len(gateway.requests) != 1 || gateway.requests[0].OrderNo != p.GatewayOrderRef || gateway.requests[0].Amount != 1050 || !strings.Contains(gateway.requests[0].NotifyBaseURL, fmt.Sprintf("channel_id=%d", ch.ID)) {
				t.Fatal("incorrect gateway request")
			}
			form := url.Values{"appid": {"test-app"}, "trade_order_id": {p.GatewayOrderRef}, "transaction_id": {"T-recharge"}, "total_fee": {"10.50"}, "status": {"OD"}}
			srv := khttp.NewServer()
			RegisterPaymentCallback(srv, repo, d)
			send := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", fmt.Sprintf("/payments/callback/hpj?channel_id=%d", ch.ID), strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				return rec
			}
			hpjTestSign(form)
			form.Set("hash", "bad")
			if rec := send(); rec.Code != 401 {
				t.Fatalf("bad signature accepted %d", rec.Code)
			}
			form.Set("total_fee", "10.00")
			hpjTestSign(form)
			if rec := send(); rec.Code == 200 {
				t.Fatal("wrong signed amount accepted")
			}
			form.Set("total_fee", "10.50")
			form.Set("status", "RD")
			hpjTestSign(form)
			if rec := send(); rec.Code != 200 || rec.Body.String() != "success" {
				t.Fatalf("non-paid ack %d %s", rec.Code, rec.Body.String())
			}
			if d.Client.Payment.GetX(ctx, p.ID).Status != "pending" || supplier.calls != 0 {
				t.Fatal("non-paid callback credited account")
			}
			form.Set("status", "OD")
			form.Set("trade_order_id", "ZPunknown")
			hpjTestSign(form)
			if rec := send(); rec.Code == 200 {
				t.Fatal("unknown reference accepted")
			}
			form.Set("trade_order_id", p.GatewayOrderRef)
			hpjTestSign(form)
			for i := 0; i < 2; i++ {
				if rec := send(); rec.Code != 200 || rec.Body.String() != "success" {
					t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
				}
			}
			if d.Client.Payment.GetX(ctx, p.ID).Status != "success" || d.Client.RechargeOrder.GetX(ctx, ro.ID).Status != "success" {
				t.Fatal("recharge not settled")
			}
			if target == rechargeorder.TargetBalance {
				available, _, err := wallet.GetBalance(ctx, 1)
				if err != nil || available != 1100 {
					t.Fatalf("balance %d %v", available, err)
				}
			} else if supplier.calls != 1 || supplier.amount != 1100 {
				t.Fatalf("supplier credit %+v", supplier)
			}
		})
	}
}

func TestXunhupayDispatchDeviceAndCache(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	_, p := seedPendingOrder(t, d, "hpj", 1000)
	gateway := &hpjTestGateway{XunhupayAdapter: adapter.NewXunhupay()}
	srv := khttp.NewServer()
	srv.Route("/").GET("/test", func(ctx khttp.Context) error {
		info, err := repo.dispatchPayment(ctx, p, gateway, port.CreatePaymentRequest{OrderNo: "ZPdevice"})
		if err != nil {
			return err
		}
		return ctx.JSON(200, info)
	})
	for _, tc := range []struct{ ua, kind string }{{"Mozilla/5.0", "qrcode"}, {"iPhone Mobile", "redirect"}, {"Android", "redirect"}, {"iPad", "redirect"}, {"Mozilla/5.0", "qrcode"}} {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("User-Agent", tc.ua)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var info port.RedirectInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || rec.Code != 200 || info.Type != tc.kind {
			t.Fatalf("%s: %d %s %v", tc.ua, rec.Code, rec.Body.String(), err)
		}
		var payload map[string]string
		_ = json.Unmarshal(info.Payload, &payload)
		if tc.kind == "qrcode" && payload["code_url"] != "https://pay.example/qr.png" || tc.kind == "redirect" && payload["url"] != "https://pay.example/mobile" {
			t.Fatalf("wrong payload %v", payload)
		}
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("duplicate gateway orders: %d", len(gateway.requests))
	}
	if _, err := paymentForDevice(ctx, "xunhupay", &port.RedirectInfo{Deadline: time.Now().Add(-time.Second)}); err == nil {
		t.Fatal("expired QR reused")
	}
}

func TestXunhupayAdminConfiguration(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	admin := NewAdminPaymentService(repo, d)
	drivers, err := admin.ListDrivers(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, driver := range drivers.Drivers {
		if driver.Code == "xunhupay" {
			found = true
		}
	}
	if !found {
		t.Fatal("虎皮椒 missing from driver catalog")
	}
	ch, err := admin.CreateChannel(ctx, &adminv1.CreateChannelRequest{Name: "虎皮椒微信", Code: "hpj", Driver: "xunhupay", ConfigJson: `{"appid":"test-app","appsecret":"test-secret"}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ch.ConfigJson, "test-secret") {
		t.Fatal("secret exposed in admin response")
	}
	_, err = admin.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, ConfigJson: `{"appid":"test-app","appsecret":""}`})
	if err != nil {
		t.Fatal(err)
	}
	stored := d.Client.PaymentChannel.GetX(ctx, ch.Id)
	if !strings.Contains(string(repo.DecryptConfig(stored)), "test-secret") {
		t.Fatal("blank edit erased secret")
	}
}
