package reseller

// 分站自营商品上架链路测试（ 验收旅程出单段首环）：
// 上架 API（等级权限位）→ 商品落本人分站（subsite_id = profile.ID）
// → 分站内 slug 唯一、分站间互不冲突。

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"

	"github.com/go-kratos/kratos/v3/errors"
)

// TestCreateOwnProduct 商品行：落本人分站 + 分站内 slug 唯一 + 分站间隔离。
func TestCreateOwnProduct(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)

	p, err := r.CreateOwnProduct(ctx, subsite, OwnProductInput{Name: "分站商品A", Price: 1000, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.SubsiteID != subsite {
		t.Fatalf("应落本人分站: %d != %d", p.SubsiteID, subsite)
	}
	if p.Status != 1 || p.Price != 1000 {
		t.Fatalf("字段错误: status=%d price=%d", p.Status, p.Price)
	}

	// 同分站重名 → slug 递增不冲突
	p2, err := r.CreateOwnProduct(ctx, subsite, OwnProductInput{Name: "分站商品A", Price: 2000, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Slug == p.Slug {
		t.Fatal("同分站同 slug 应递增")
	}

	// 第二分站同名 → slug 不冲突（唯一性按 subsite 维度）
	if _, err := d.Client.User.Create().SetUsername("owner2").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	app, err := r.Apply(ctx, ApplyInput{UserID: 2, Reason: "开店"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Review(ctx, app.ID, true, "", 99, 10, 50, 7); err != nil {
		t.Fatal(err)
	}
	p3, err := r.CreateOwnProduct(ctx, app.ID, OwnProductInput{Name: "分站商品A", Price: 1000, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p3.Slug != p.Slug {
		t.Fatalf("分站间同名商品 slug 应互不影响: %s != %s", p3.Slug, p.Slug)
	}
}

// TestAdminCreateProduct 服务面：非分站主拒绝 / 分站主上架落本人分站 / 等级权限位。
func TestAdminCreateProduct(t *testing.T) {
	r, d := newResellerData(t)
	svc := NewAdminResellerService(r)
	ctx := context.Background()
	subsite := seedApproved(t, r, d) // 站主 user 1

	// 非分站主（无 claims）→ Forbidden
	if _, err := svc.CreateProduct(ctx, &adminv1.CreateResellerProductRequest{
		Name: "x", PriceCents: 100,
	}); !errors.IsForbidden(err) {
		t.Fatalf("非分站主应拒绝: %v", err)
	}

	// 分站主（claims.Subject=1）→ 上架成功，商品落本人分站
	ownerCtx := identity.WithClaims(ctx, &authn.Claims{Subject: 1})
	p, err := svc.CreateProduct(ownerCtx, &adminv1.CreateResellerProductRequest{
		Name: "自营商品", PriceCents: 1000, Status: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.SubsiteId != subsite {
		t.Fatalf("商品应落本人分站: %d != %d", p.SubsiteId, subsite)
	}
	if p.PriceCents != 1000 || p.Status != 1 {
		t.Fatalf("回执字段错误: %+v", p)
	}

	// 等级权限位：level=0 → 拒绝自助上架
	if _, err := d.Client.ResellerProfile.UpdateOneID(subsite).SetLevel(0).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProduct(ownerCtx, &adminv1.CreateResellerProductRequest{
		Name: "y", PriceCents: 100,
	}); !errors.IsForbidden(err) {
		t.Fatalf("等级 0 应拒绝自助上架: %v", err)
	}
}
