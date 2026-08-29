package wallet

// 充值 fail-closed 与调账边界测试（铁律 16：金额权威在后端——抓包改金额拦截验证）。

import (
	"context"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	paymentport "github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"github.com/go-kratos/kratos/v3/errors"
)

// fakePayer 充值支付创建假实现（记录调用，返回固定 payment_id）。
type fakePayer struct {
	calls int
}

func (f *fakePayer) CreateRechargePayment(_ context.Context, rechargeOrderID uint64, channel, method string, _ money.Cents) (*paymentport.RechargePaymentInfo, error) {
	f.calls++
	return &paymentport.RechargePaymentInfo{PaymentID: 900 + uint64(f.calls), Type: "redirect", Payload: "https://pay.example/1"}, nil
}

// fakeSettings 充值档位假实现（settings.recharge 组键值）。
type fakeSettings struct{ m map[string][]byte }

func (f *fakeSettings) GetJSON(_ context.Context, group, key string) ([]byte, error) {
	if f.m == nil {
		return nil, nil
	}
	return f.m[group+"/"+key], nil
}

func newRechargeSettings(t *testing.T, enabled bool, minA, maxA int64, tiers string) *fakeSettings {
	t.Helper()
	m := map[string][]byte{}
	write := func(k string, v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		m[k] = raw
	}
	write("recharge/enabled", enabled)
	write("recharge/min_amount", minA)
	write("recharge/max_amount", maxA)
	if tiers != "" {
		m["recharge/gift_tiers"] = []byte(tiers)
	}
	return &fakeSettings{m: m}
}

func newStoreWalletService(t *testing.T, payer paymentport.RechargePayer) (*StoreWalletService, *WalletRepoImpl) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	// 支付管线未装配（payer=nil）——充值 fail-closed 测试只验证档位与零入账
	return NewStoreWalletService(repo, d, nil, payer, nil, nil), repo
}

func userCtx(uid uint64) context.Context {
	return identity.WithClaims(context.Background(), &authn.Claims{Subject: uid, Realm: authn.RealmUser})
}

// TestRechargePolicy 档位裁决：停用/超下限/超上限/超全局上限全部拒绝。
func TestRechargePolicy(t *testing.T) {
	svc, _ := newStoreWalletService(t, &fakePayer{})
	ctx := userCtx(1)

	cases := []struct {
		name     string
		settings *fakeSettings
		amount   int64
		wantErr  bool
	}{
		{"停用拒绝", newRechargeSettings(t, false, 1000, 500000, ""), 1000, true},
		{"低于下限", newRechargeSettings(t, true, 1000, 500000, ""), 999, true},
		{"高于上限", newRechargeSettings(t, true, 1000, 500000, ""), 500001, true},
		{"超全局上限", newRechargeSettings(t, true, 1000, money.MaxCents, ""), money.MaxCents + 1, true},
		{"档位内放行", newRechargeSettings(t, true, 1000, 500000, ""), 10000, false},
	}
	for _, c := range cases {
		svc.settings = c.settings
		_, err := svc.CreateRecharge(ctx, &storefrontv1.CreateRechargeRequest{AmountCents: c.amount, Channel: "alipay"})
		if c.wantErr != (err != nil) {
			t.Errorf("%s: err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

// TestRechargeFailClosed 核心安全断言：创建充值单绝不直接入账（支付确认前零入账）。
func TestRechargeFailClosed(t *testing.T) {
	svc, repo := newStoreWalletService(t, &fakePayer{})
	svc.settings = newRechargeSettings(t, true, 1000, 500000,
		`[{"amount":10000,"gift_balance":500,"gift_points":100}]`)
	ctx := userCtx(1)

	reply, err := svc.CreateRecharge(ctx, &storefrontv1.CreateRechargeRequest{AmountCents: 10000, Channel: "alipay"})
	if err != nil {
		t.Fatal(err)
	}
	// 充值单 pending，赠送服务端算定
	ro, err := repo.data.Client.RechargeOrder.Get(ctx, reply.RechargeId)
	if err != nil {
		t.Fatal(err)
	}
	if string(ro.Status) != "pending" || ro.GiftAmount != 500 || ro.GiftPoints != 100 {
		t.Fatalf("充值单错误: %+v", ro)
	}
	// 钱包零变动：抓包改金额的入账路径已物理移除
	avail, _, _ := repo.GetBalance(ctx, 1)
	if avail != 0 {
		t.Fatalf("支付确认前发生入账（漏洞）: avail=%d", avail)
	}
	txCount, _ := repo.data.Client.WalletTransaction.Query().Count(ctx)
	if txCount != 0 {
		t.Fatalf("支付确认前产生流水（漏洞）: %d", txCount)
	}
}

// TestRechargeRequireLogin 未登录拒绝。
func TestRechargeRequireLogin(t *testing.T) {
	svc, _ := newStoreWalletService(t, &fakePayer{})
	_, err := svc.CreateRecharge(context.Background(), &storefrontv1.CreateRechargeRequest{AmountCents: 1000})
	if !errors.IsUnauthorized(err) {
		t.Fatalf("未登录应拒绝: %v", err)
	}
}

// TestAdjustBounds 调账边界：零金额/超上限拒绝。
func TestAdjustBounds(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	svc := NewAdminWalletService(repo, d, nil)
	ctx := context.Background()

	if _, err := svc.Adjust(ctx, &adminv1.AdjustRequest{UserId: 1, AmountCents: 0, Reason: "r"}); !errors.IsBadRequest(err) {
		t.Fatalf("零金额应拒绝: %v", err)
	}
	if _, err := svc.Adjust(ctx, &adminv1.AdjustRequest{UserId: 1, AmountCents: money.MaxCents + 1, Reason: "r"}); !errors.IsBadRequest(err) {
		t.Fatalf("超上限应拒绝: %v", err)
	}
	// 先入账再扣减——验证合法有符号金额路径畅通
	if err := repo.CreditInTx(ctx, Entry{UserID: 1, Direction: "in", Type: "adjust", Amount: 1000, Reference: "seed:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adjust(ctx, &adminv1.AdjustRequest{UserId: 1, AmountCents: -100, Reason: "r"}); err != nil {
		t.Fatalf("合法扣减被拒绝: %v", err)
	}
}
