package payment

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
)

func TestDeleteDisabledBepusdtPreservesCallbacks(t *testing.T) {
	for _, state := range []string{"pending_payment", "canceled"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			d, repo, _, _, life, _ := newCallbackEnv(t)
			ch := beTestChannel(t, repo, "https://pay.example")
			o, p := seedPendingOrder(t, d, ch.Code, 1000)
			if state == "canceled" {
				d.Client.Order.UpdateOneID(o.ID).SetStatus("canceled").SaveX(ctx)
			}
			p = d.Client.Payment.UpdateOneID(p.ID).SetChannelID(ch.ID).SetDriverSnapshot("bepusdt").SetGatewayOrderRef("BE-delete-test").SetExpiresAt(time.Now().Add(10 * time.Minute)).SaveX(ctx)
			if err := repo.DeleteChannel(ctx, ch.ID); err == nil || !strings.Contains(err.Error(), "CHANNEL_ENABLED") {
				t.Fatalf("enabled channel deletion: %v", err)
			}
			if _, err := repo.UpdateChannel(ctx, ch.ID, "", "", -1, "", false, -1, false, "", false, nil); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := repo.DeleteChannel(ctx, ch.ID); err != nil {
					t.Fatal(err)
				}
			}
			stored := d.Client.PaymentChannel.GetX(ctx, ch.ID)
			if stored.DeletedAt.IsZero() || stored.Enabled || !bytes.Equal(stored.Config, ch.Config) || stored.Code != ch.Code {
				t.Fatal("deleted channel lost callback identity or credentials")
			}
			rows, err := repo.ListChannels(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.ID == ch.ID {
					t.Fatal("deleted channel is still in admin list")
				}
			}
			public, err := NewStorePaymentService(repo, d).ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range public.Channels {
				if row.Code == ch.Code {
					t.Fatal("deleted channel is still offered at checkout")
				}
			}
			if _, err := repo.UpdateChannel(ctx, ch.ID, "revived", "", -1, "", true, -1, false, "", false, nil); err == nil || !strings.Contains(err.Error(), "CHANNEL_DELETED") {
				t.Fatalf("deleted channel can be revived: %v", err)
			}
			if _, err := repo.createBepusdtPayment(ctx, ch.ID, o.ID, 0, ""); err == nil || !strings.Contains(err.Error(), "CHANNEL_DISABLED") {
				t.Fatalf("deleted channel accepted new payment: %v", err)
			}
			// Replacing a deleted card must not reuse its code, ID or callback token.
			replacement, err := repo.CreateChannel(ctx, "Replacement", ch.Code, ch.Driver, `{"api_url":"https://new.example","api_token":"replacement-token","trade_type":"usdt.trc20","timeout":600}`, 0, "fixed", false, 0, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if replacement.Code == ch.Code || replacement.ID == ch.ID || !strings.Contains(string(repo.DecryptConfig(replacement)), "replacement-token") {
				t.Fatal("replacement reused historical channel identity")
			}
			body := beTestCallbackBody(p.GatewayOrderRef, "trade-delete", 10, 2)
			for i := 0; i < 2; i++ {
				rec := beServeCallback(d, repo, ch, body)
				if rec.Code != 200 || rec.Body.String() != "success" {
					t.Fatalf("historical callback failed: %d %s", rec.Code, rec.Body.String())
				}
			}
			got := d.Client.Payment.GetX(ctx, p.ID)
			if got.Status != "success" || got.ChannelID != ch.ID || got.ChargedAmount != 1000 {
				t.Fatal("historical payment was lost or assigned to replacement")
			}
			if state == "canceled" {
				if got.ReviewReason == "" || len(life.markPaidCalls) != 0 {
					t.Fatal("late payment on canceled order must remain under review")
				}
			} else if len(life.markPaidCalls) != 1 {
				t.Fatal("callback must settle pending order exactly once")
			}
		})
	}
}

func TestDeleteUnusedDisabledBepusdt(t *testing.T) {
	ctx := context.Background()
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ch := beTestChannel(t, repo, "https://pay.example")
	d.Client.PaymentChannel.UpdateOneID(ch.ID).SetEnabled(false).SaveX(ctx)
	if err := repo.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.PaymentChannel.GetX(ctx, ch.ID).DeletedAt.IsZero() {
		t.Fatal("unused disabled channel was not deleted")
	}
}
