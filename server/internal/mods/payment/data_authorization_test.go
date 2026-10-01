package payment

import (
	"context"
	"testing"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestStorePaymentOrderAuthorization(t *testing.T) {
	hash, err := crypto.HashPassword("buyer-query-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, channel, password           string
		buyer, requester, site, orderSite uint64
		legacy, allowed, omitQuote        bool
	}{
		{name: "guest cannot spend owner wallet", channel: "balance", buyer: 1},
		{name: "guest cannot bypass wallet authorization by omitting quote", channel: "balance", buyer: 1, omitQuote: true},
		{name: "query password cannot authorize guest wallet", channel: "balance", buyer: 1, password: "buyer-query-password"},
		{name: "another member cannot spend owner wallet", channel: "balance", buyer: 1, requester: 2},
		{name: "query password cannot authorize another member wallet", channel: "balance", buyer: 1, requester: 2, password: "buyer-query-password"},
		{name: "wallet owner needs no password or quote", channel: "balance", buyer: 1, requester: 1, allowed: true, omitQuote: true},
		{name: "wallet owner ignores incorrect query password", channel: "balance", buyer: 1, requester: 1, password: "wrong", allowed: true},
		{name: "guest order has no wallet owner", channel: "balance", password: "buyer-query-password", requester: 1},
		{name: "legacy wallet owner still pays", channel: "balance", buyer: 1, requester: 1, legacy: true, allowed: true},
		{name: "wallet owner cannot cross tenants", channel: "balance", buyer: 1, requester: 1, site: 8, password: "buyer-query-password"},
		{name: "wallet owner can pay within tenant", channel: "balance", buyer: 1, requester: 1, site: 8, orderSite: 8, allowed: true},
		{name: "guest external payment requires password", channel: "epay", buyer: 1},
		{name: "guest external payment rejects incorrect password", channel: "epay", buyer: 1, password: "wrong"},
		{name: "guest external payment accepts query password", channel: "epay", buyer: 1, password: "buyer-query-password", allowed: true},
		{name: "another member external payment requires password", channel: "epay", buyer: 1, requester: 2},
		{name: "another member external payment accepts query password", channel: "epay", buyer: 1, requester: 2, password: "buyer-query-password", allowed: true},
		{name: "external owner needs no password", channel: "epay", buyer: 1, requester: 1, allowed: true},
		{name: "guest order external payment accepts query password", channel: "epay", password: "buyer-query-password", allowed: true},
		{name: "legacy guest order has no payment authorization", channel: "epay", legacy: true, password: "buyer-query-password"},
		{name: "legacy external owner still pays", channel: "epay", buyer: 1, requester: 1, legacy: true, allowed: true},
		{name: "legacy member order rejects other member", channel: "epay", buyer: 1, requester: 2, legacy: true},
		{name: "external owner cannot cross tenants", channel: "epay", buyer: 1, requester: 1, site: 8},
		{name: "external query password cannot cross tenants", channel: "epay", buyer: 1, site: 8, password: "buyer-query-password"},
		{name: "guest external payment can use password within tenant", channel: "epay", buyer: 1, site: 8, orderSite: 8, password: "buyer-query-password", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, repo, ledger, writer, lifecycle, _ := newCallbackEnv(t)
			ctx := tenancy.WithContext(context.Background(), tenancy.Context{SubsiteID: tc.site})
			if tc.requester != 0 {
				ctx = identity.WithClaims(ctx, &authn.Claims{Subject: tc.requester, Realm: authn.RealmUser})
			}
			if err := ledger.CreditInTx(ctx, wallet.Entry{UserID: 1, Direction: "in", Type: "adjust", Amount: 5000, Reference: "authorization-seed"}); err != nil {
				t.Fatal(err)
			}
			create := d.Client.Order.Create().SetOrderNo("AUTHORIZATION-ORDER").SetUserID(tc.buyer).SetSubsiteID(tc.orderSite).
				SetTotalAmount(1300).SetShippingAmount(300).SetExpiredAt(time.Now().Add(time.Hour))
			if !tc.legacy {
				create.SetCommerceVersion(1).SetQueryPasswordHash(hash)
			}
			o := create.SaveX(ctx)
			svc := NewStorePaymentService(repo, d)
			// A valid owner's quote is deliberately available to every test caller.
			// Possessing this optimistic price key must never authorize payment.
			quoteKey := ""
			if tc.buyer != 0 && !tc.omitQuote {
				owner := identity.WithClaims(tenancy.WithContext(context.Background(), tenancy.Context{SubsiteID: tc.orderSite}), &authn.Claims{Subject: tc.buyer, Realm: authn.RealmUser})
				q, err := svc.QuotePayment(owner, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: tc.channel})
				if err != nil {
					t.Fatal("owner quote", err)
				}
				quoteKey = q.QuoteKey
			}
			q, err := svc.QuotePayment(ctx, &storefrontv1.PaymentQuoteRequest{OrderNo: o.OrderNo, Channel: tc.channel, QueryPassword: tc.password})
			if (err == nil) != tc.allowed {
				t.Fatalf("quote allowed=%v: quote=%+v error=%v", tc.allowed, q, err)
			}
			if q != nil && q.TotalCents != 1300 {
				t.Fatalf("quote changed order amount: %+v", q)
			}
			result, err := svc.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: tc.channel, QueryPassword: tc.password, QuoteKey: quoteKey})
			if (err == nil) != tc.allowed {
				t.Fatalf("create allowed=%v: result=%+v error=%v", tc.allowed, result, err)
			}
			balance, _, err := ledger.GetBalance(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.allowed {
				if balance != 5000 || d.Client.Payment.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 || len(lifecycle.markPaidCalls) != 0 || len(writer.evts) != 0 || d.Client.Order.GetX(ctx, o.ID).Version != o.Version {
					t.Fatal("unauthorized payment changed wallet, order, payment records or events")
				}
				return
			}
			paid := d.Client.Payment.GetX(ctx, result.PaymentId)
			if tc.channel == "balance" {
				if balance != 3700 || paid.Status != payment.StatusSuccess || len(lifecycle.markPaidCalls) != 1 {
					t.Fatal("authorized wallet payment failed to debit and settle")
				}
			} else if balance != 5000 || paid.Status != payment.StatusPending || len(lifecycle.markPaidCalls) != 0 {
				t.Fatal("external payment affected wallet or settled before callback")
			}
		})
	}
}
