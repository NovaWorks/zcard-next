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

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestEpayIndependentChannels(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, _, _ := newCallbackEnv(t)
	admin := NewAdminPaymentService(repo, d)
	store := NewStorePaymentService(repo, d)
	second, err := admin.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "第二上游", Code: "epay-2", Driver: "epay", Enabled: true,
		ConfigJson:  `{"pid":"2000","key":"second-key","api_url":"https://second.example/submit.php"}`,
		MethodsJson: `[{"code":"alipay","name":"支付宝","enabled":true,"params":{"type":"alipay"}}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.ConfigJson, "second-key") {
		t.Fatal("secret exposed")
	}
	list, err := store.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{Scene: scenePurchase})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, ch := range list.Channels {
		found[ch.Code] = true
	}
	if !found["epay"] || !found["epay-2"] {
		t.Fatal("both channels must be selectable")
	}
	srv := khttp.NewServer()
	RegisterPaymentCallback(srv, repo, d)
	for _, tc := range []struct{ code, pid, key, gateway, method string }{
		{"epay", "1000", "testkey", "https://pay.epay.com/submit.php", ""},
		{"epay-2", "2000", "second-key", "https://second.example/submit.php", "alipay"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			o, _ := seedPendingOrder(t, d, tc.code, 1000)
			info, err := store.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: tc.code, Method: tc.method})
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				URL    string            `json:"url"`
				Params map[string]string `json:"params"`
			}
			if err = json.Unmarshal([]byte(info.Payload), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.URL != tc.gateway || payload.Params["pid"] != tc.pid {
				t.Fatalf("wrong upstream: %+v", payload)
			}
			p := d.Client.Payment.GetX(ctx, info.PaymentId)
			if p.Channel != tc.code || p.ChannelID == 0 || p.DriverSnapshot != "epay" {
				t.Fatal("channel identity not saved")
			}
			notify := fmt.Sprintf("/payments/callback/%s?channel_id=%d", tc.code, p.ChannelID)
			if !strings.HasSuffix(payload.Params["notify_url"], notify) {
				t.Fatalf("wrong callback: %s", payload.Params["notify_url"])
			}
			form := url.Values{"pid": {tc.pid}, "out_trade_no": {payload.Params["out_trade_no"]}, "trade_no": {"trade-" + tc.code}, "money": {"10.00"}, "trade_status": {"TRADE_SUCCESS"}, "type": {"alipay"}}
			sign := func(key string) {
				keys := make([]string, 0, len(form))
				for k := range form {
					if k != "sign" {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, k+"="+form.Get(k))
				}
				form.Set("sign", fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "&")+key))))
			}
			send := func(target string) *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				return rec
			}
			sign("wrong-upstream-key")
			if rec := send(notify); rec.Code != 401 {
				t.Fatalf("wrong key accepted: %d %s", rec.Code, rec.Body.String())
			}
			if d.Client.Payment.GetX(ctx, p.ID).Status != "pending" {
				t.Fatal("invalid callback settled payment")
			}
			otherCode := "epay-2"
			otherKey := "second-key"
			if tc.code == otherCode {
				otherCode = "epay"
				otherKey = "testkey"
			}
			// Even a valid signature for the other upstream cannot settle this order.
			sign(otherKey)
			if rec := send("/payments/callback/" + otherCode); rec.Body.String() == "success" {
				t.Fatal("wrong channel accepted callback")
			}
			if d.Client.Payment.GetX(ctx, p.ID).Status != "pending" {
				t.Fatal("other channel settled payment")
			}
			sign(tc.key)
			for i := 0; i < 2; i++ {
				if rec := send(notify); rec.Code != 200 || rec.Body.String() != "success" {
					t.Fatalf("callback failed: %d %s", rec.Code, rec.Body.String())
				}
			}
			if d.Client.Payment.GetX(ctx, p.ID).Status != "success" {
				t.Fatal("valid callback not settled")
			}
		})
	}
}
