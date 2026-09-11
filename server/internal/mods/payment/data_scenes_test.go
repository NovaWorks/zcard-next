package payment

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"strings"
	"testing"
	"time"
)

func TestPaymentUsageMatrixAndServerEnforcement(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	svc := NewStorePaymentService(repo, d)
	ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
	for mask := 0; mask < 8; mask++ {
		ch = d.Client.PaymentChannel.UpdateOneID(ch.ID).SetAllowPurchase(mask&1 != 0).SetAllowMemberRecharge(mask&2 != 0).SetAllowSupplyRecharge(mask&4 != 0).SaveX(ctx)
		for i, scene := range []string{scenePurchase, sceneMemberRecharge, sceneSupplyRecharge} {
			allowed := mask&(1<<i) != 0
			list, err := svc.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{Scene: scene})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range list.Channels {
				if item.Code == "epay" {
					found = true
				}
			}
			if found != allowed {
				t.Fatalf("mask=%d scene=%s listed=%v", mask, scene, found)
			}
			before := d.Client.Payment.Query().CountX(ctx)
			if scene == scenePurchase {
				o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("SCENE-%d", mask)).SetTotalAmount(100).SetExpiredAt(time.Now().Add(time.Hour)).SaveX(ctx)
				_, err = svc.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: "epay"})
			} else {
				target := rechargeorder.TargetBalance
				if scene == sceneSupplyRecharge {
					target = rechargeorder.TargetSupply
				}
				ro := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(100).SetTarget(target).SaveX(ctx)
				_, err = repo.CreateRechargePayment(ctx, ro.ID, "epay", "", 100)
			}
			if allowed && err != nil {
				t.Fatalf("allowed %s: %v", scene, err)
			}
			if !allowed && (err == nil || !strings.Contains(err.Error(), "SCENE_DISABLED")) {
				t.Fatalf("blocked %s: %v", scene, err)
			}
			if !allowed && d.Client.Payment.Query().CountX(ctx) != before {
				t.Fatal("blocked request created payment")
			}
		}
	}
	if _, err := svc.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{Scene: "fake"}); err == nil {
		t.Fatal("invalid scene accepted")
	}
}
func TestAggregateMethodUsageAndLegacyUpdates(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	svc := NewStorePaymentService(repo, d)
	ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
	ms, err := methodsJSON(`[{"code":"alipay","name":"支付宝","enabled":true,"allow_supply_recharge":false},{"code":"wxpay","name":"微信","enabled":true}]`)
	if err != nil {
		t.Fatal(err)
	}
	ch = d.Client.PaymentChannel.UpdateOneID(ch.ID).SetMethods(ms).SaveX(ctx)
	list, err := svc.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{Scene: sceneSupplyRecharge})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Channels {
		if item.Code == "epay" && (len(item.Methods) != 1 || item.Methods[0].Code != "wxpay") {
			t.Fatal(item)
		}
	}
	ro := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(100).SetTarget(rechargeorder.TargetSupply).SaveX(ctx)
	if _, err = repo.CreateRechargePayment(ctx, ro.ID, "epay", "alipay", 100); err == nil || !strings.Contains(err.Error(), "SCENE_DISABLED") {
		t.Fatal(err)
	}
	if _, err = repo.CreateRechargePayment(ctx, ro.ID, "epay", "", 100); err == nil {
		t.Fatal("omitted method bypass")
	}
	if _, err = repo.CreateRechargePayment(ctx, ro.ID, "epay", "wxpay", 100); err != nil {
		t.Fatal(err)
	}
	// All methods filtered: never expose the channel as a legacy single-method fallback.
	ms, err = methodsJSON(`[{"code":"alipay","name":"支付宝","enabled":true,"allow_supply_recharge":false}]`)
	if err != nil {
		t.Fatal(err)
	}
	d.Client.PaymentChannel.UpdateOneID(ch.ID).SetMethods(ms).ExecX(ctx)
	list, err = svc.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{Scene: sceneSupplyRecharge})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Channels {
		if item.Code == "epay" {
			t.Fatal("empty methods became channel fallback")
		}
	}
	no := false
	admin := NewAdminPaymentService(repo, d)
	if _, err = admin.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.ID, Enabled: true, AllowSupplyRecharge: &no}); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.ID, Name: "重命名", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ch = d.Client.PaymentChannel.GetX(ctx, ch.ID)
	if ch.AllowSupplyRecharge || !ch.AllowPurchase || !ch.AllowMemberRecharge {
		t.Fatal("legacy write reset policy")
	}
	if !strings.Contains(ToChannelPB(ch).MethodsJson, "allow_supply_recharge") {
		t.Fatal("lost method policy")
	}
	if _, err = methodsJSON(`[{"code":"a","name":"a"},{"code":"a","name":"b"}]`); err == nil {
		t.Fatal("duplicate method codes")
	}
}

func TestExistingRechargeStillSettlesAfterUsageDisabled(t *testing.T) {
	d, repo, walletRepo, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	ro := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(100).SaveX(ctx)
	info, err := repo.CreateRechargePayment(ctx, ro.ID, "epay", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Client.PaymentChannel.Query().Where(paymentchannel.Code("epay")).OnlyX(ctx)
	d.Client.PaymentChannel.UpdateOneID(ch.ID).SetAllowMemberRecharge(false).ExecX(ctx)
	fact := CallbackFact{Channel: "epay", ChannelOrderNo: "existing-recharge", Amount: 100, Currency: "CNY", Success: true}
	if err := repo.HandleCallback(ctx, info.PaymentID, fact); err != nil {
		t.Fatal(err)
	}
	if err := repo.HandleCallback(ctx, info.PaymentID, fact); err != nil {
		t.Fatal(err)
	}
	balance, _, err := walletRepo.GetBalance(ctx, 1)
	if err != nil || balance != 100 {
		t.Fatal(balance, err)
	}
}
