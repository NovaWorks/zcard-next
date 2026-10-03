package payment

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/orderaccess"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"testing"
	"time"
)

func TestGuestPhysicalPaymentTokenAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, channel string
		valid         bool
		site          uint64
		allowed       bool
	}{
		{"external token", "epay", true, 0, true},
		{"missing token", "epay", false, 0, false},
		{"cross tenant", "epay", true, 3, false},
		{"token cannot spend member wallet", "balance", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, repo, _, _, lifecycle, _ := newCallbackEnv(t)
			token, err := orderaccess.NewToken()
			if err != nil {
				t.Fatal(err)
			}
			ctx := tenancy.WithContext(context.Background(), tenancy.Context{SubsiteID: tc.site})
			if tc.channel == "balance" {
				ctx = identity.WithClaims(ctx, &authn.Claims{Subject: 1, Realm: authn.RealmUser})
			}
			o := d.Client.Order.Create().SetOrderNo("GUEST-PHYSICAL-PAY").SetCommerceVersion(1).SetTotalAmount(1300).SetExpiredAt(time.Now().Add(time.Hour)).SetOrderAccessTokenHash(orderaccess.TokenHash(0, "GUEST-PHYSICAL-PAY", token)).SaveX(ctx)
			svc := NewStorePaymentService(repo, d)
			access := ""
			if tc.valid {
				access = token
			}
			q, err := svc.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: tc.channel, OrderAccessToken: access})
			if (err == nil) != tc.allowed {
				t.Fatalf("quote authorization mismatch: %v", err)
			}
			key := ""
			if q != nil {
				key = q.QuoteKey
			}
			_, err = svc.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: tc.channel, OrderAccessToken: access, QuoteKey: key})
			if (err == nil) != tc.allowed {
				t.Fatalf("payment authorization mismatch: %v", err)
			}
			if !tc.allowed && (d.Client.Payment.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 || len(lifecycle.markPaidCalls) != 0) {
				t.Fatal("unauthorized credential changed payment or wallet")
			}
		})
	}
}
