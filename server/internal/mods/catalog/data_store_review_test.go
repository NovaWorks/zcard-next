package catalog

import (
	"context"
	"strings"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

func TestOrderReviewAuthorizationAndModeration(t *testing.T) {
	d, repo := newMaintainEnv(t)
	s := NewStoreReviewService(repo)
	base := context.Background()
	ctx := identity.WithClaims(base, &authn.Claims{Subject: 7, Realm: authn.RealmUser})
	p := d.Client.Product.Create().SetName("已购商品").SetSlug("review-product").SetPrice(100).SaveX(base)
	o := d.Client.Order.Create().SetOrderNo("review-order").SetUserID(7).SetStatus(order.StatusDelivered).SetTotalAmount(100).SaveX(base)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("auto").SaveX(base)
	get := &storefrontv1.GetOrderReviewRequest{OrderNo: o.OrderNo}
	for _, tc := range []struct {
		ctx  context.Context
		code int32
	}{
		{base, 401}, {identity.WithClaims(base, &authn.Claims{Subject: 8}), 404}, {tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99}), 404},
	} {
		if _, err := s.GetOrderReview(tc.ctx, get); errors.Code(err) != int(tc.code) {
			t.Fatalf("authorization: %v", err)
		}
	}
	state, err := s.GetOrderReview(ctx, get)
	if err != nil || state.Status != "available" || len(state.Products) != 1 || state.Products[0].Name != p.Name {
		t.Fatalf("eligible: %v %v", state, err)
	}
	submit := &storefrontv1.SubmitOrderReviewRequest{OrderNo: o.OrderNo, ProductId: p.ID, Rating: 5, Content: "使用方便"}
	for _, bad := range []struct {
		rating  int32
		content string
		product uint64
	}{{0, "好", p.ID}, {6, "好", p.ID}, {5, "   ", p.ID}, {5, strings.Repeat("中", 1001), p.ID}, {5, "好", p.ID + 99}} {
		req := *submit
		req.Rating = bad.rating
		req.Content = bad.content
		req.ProductId = bad.product
		if _, err := s.SubmitOrderReview(ctx, &req); errors.Code(err) != 400 {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	for _, status := range []order.Status{order.StatusPendingPayment, order.StatusPaid, order.StatusRefunded} {
		d.Client.Order.UpdateOneID(o.ID).SetStatus(status).ExecX(base)
		if _, err := s.SubmitOrderReview(ctx, submit); errors.Code(err) != 409 {
			t.Fatalf("invalid status %s: %v", status, err)
		}
	}
	d.Client.Order.UpdateOneID(o.ID).SetStatus(order.StatusDelivered).ExecX(base)
	cfg := d.Client.Setting.Create().SetGroup("template").SetKey("show_reviews").SetValue([]byte("false")).SaveX(base)
	if _, err := s.SubmitOrderReview(ctx, submit); errors.Code(err) != 403 {
		t.Fatalf("disabled: %v", err)
	}
	if d.Client.Review.Query().CountX(base) != 0 {
		t.Fatal("invalid submission wrote review")
	}
	d.Client.Setting.UpdateOneID(cfg.ID).SetValue([]byte("true")).ExecX(base)
	state, err = s.SubmitOrderReview(ctx, submit)
	if err != nil || state.Status != "pending" {
		t.Fatalf("submit: %v %v", state, err)
	}
	if _, err = s.SubmitOrderReview(ctx, submit); errors.Code(err) != 409 {
		t.Fatalf("duplicate: %v", err)
	}
	reviews, err := repo.ListProductReviews(ctx, p.ID)
	if err != nil || len(reviews) != 0 {
		t.Fatal("pending review publicly visible")
	}
	row := d.Client.Review.Query().OnlyX(base)
	if _, err = repo.ApproveReview(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	reviews, err = repo.ListProductReviews(ctx, p.ID)
	if err != nil || len(reviews) != 1 || reviews[0].Content != submit.Content {
		t.Fatal("approved review missing")
	}
	state, err = s.GetOrderReview(ctx, get)
	if err != nil || state.Status != "approved" {
		t.Fatalf("own review: %v %v", state, err)
	}
	d.Client.Setting.UpdateOneID(cfg.ID).SetValue([]byte(`"bad"`)).ExecX(base)
	if _, err = s.GetOrderReview(ctx, get); errors.Code(err) != 500 {
		t.Fatal("invalid configuration silently enabled")
	}
}
