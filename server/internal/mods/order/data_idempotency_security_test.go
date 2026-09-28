package order

import (
	"context"
	"strings"
	"testing"

	orderent "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestP2IdempotencyOwnershipAndCanonicalRequest(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	in := CreateOrderInput{UserID: 3, QueryPassword: "secret", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}, IdempotencyKey: "p2-owned"}
	original, err := uc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*CreateOrderInput)
	}{
		{"quantity", func(v *CreateOrderInput) { v.Items = []OrderItemInput{{ProductID: 1, Quantity: 2}} }},
		{"coupon", func(v *CreateOrderInput) { v.CouponCode = "different" }},
		{"answers", func(v *CreateOrderInput) { v.ControlAnswers = map[string]string{"name": "different"} }},
		{"contact", func(v *CreateOrderInput) { v.Contact = "different" }},
		{"points", func(v *CreateOrderInput) { v.UsePoints = true }},
		{"referral", func(v *CreateOrderInput) { v.RefCode = "different" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			v := in
			change.mutate(&v)
			if _, err := uc.CreateOrder(ctx, v); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
				t.Fatal("parameter mismatch not rejected", err)
			}
		})
	}
	other := in
	other.UserID = 4
	foreign, err := uc.CreateOrder(ctx, other)
	if err != nil || foreign.OrderNo == original.OrderNo {
		t.Fatal("user namespaces mixed", err)
	}
	p := d.Client.Product.Create().SetSubsiteID(7).SetName("site").SetSlug("p2-site").SetPrice(500).SetStatus(1).SaveX(ctx)
	site := in
	site.SubsiteID = 7
	site.Items = []OrderItemInput{{ProductID: p.ID, Quantity: 1}}
	siteCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 7})
	scoped, err := uc.CreateOrder(siteCtx, site)
	if err != nil || scoped.OrderNo == original.OrderNo {
		t.Fatal("site namespaces mixed", err)
	}
	// Both transaction and unique-conflict recovery call replay: even a corrupt
	// namespace row cannot supply an order unless ownership and HMAC both match.
	k, l, f, err := uc.idempotency(in)
	if err != nil {
		t.Fatal(err)
	}
	d.Client.Order.Update().Where(orderent.OrderNo(original.OrderNo)).SetUserID(4).ExecX(ctx)
	if _, err = uc.replay(ctx, in, k, l, f); err == nil {
		t.Fatal("ownership check absent")
	}
	d.Client.Order.Update().Where(orderent.OrderNo(original.OrderNo)).SetUserID(3).SetRequestFingerprint("").ExecX(ctx)
	if _, err = uc.replay(ctx, in, k, l, f); err == nil {
		t.Fatal("missing fingerprint accepted")
	}
	d.Client.Order.Update().Where(orderent.OrderNo(original.OrderNo)).SetIdempotencyKey(l).ExecX(ctx)
	before := d.Client.Order.Query().CountX(ctx)
	if _, err = uc.CreateOrder(ctx, in); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal("legacy key accepted", err)
	}
	if d.Client.Order.Query().CountX(ctx) != before {
		t.Fatal("legacy retry created another order")
	}
}
func TestP2GuestReplayAndAnswerFallback(t *testing.T) {
	_, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	in := CreateOrderInput{QueryPassword: "guest-secret", GuestContact: "guest@example.test", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}, IdempotencyKey: "p2-guest"}
	first, err := uc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"", "wrong", "guest-secret"} {
		retry := in
		retry.QueryPassword = password
		o, err := uc.ReplayOrder(ctx, retry)
		if password == "guest-secret" {
			if err != nil || o.OrderNo != first.OrderNo {
				t.Fatal("legal guest replay denied", err)
			}
		} else if err == nil || o != nil {
			t.Fatal("guest replay leaked order")
		}
	}
	// Nil item answers inherit global answers, explicit empty answers do not.
	in.ControlAnswers = map[string]string{"required": "global"}
	_, _, a, err := uc.idempotency(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Items = []OrderItemInput{{ProductID: 1, Quantity: 1, ControlAnswers: map[string]string{}}}
	_, _, b, err := uc.idempotency(in)
	if err != nil || a == b {
		t.Fatal("distinct effective answers collapsed")
	}
	in.Items[0].ControlAnswers = map[string]string{"required": "global"}
	_, _, c, err := uc.idempotency(in)
	if err != nil || c != a {
		t.Fatal("equivalent effective answers differ")
	}
	in.Items = append(in.Items, in.Items[0])
	if _, err := uc.CreateOrder(ctx, in); err == nil {
		t.Fatal("duplicate item accepted")
	}
}
