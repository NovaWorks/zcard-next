package catalog

// P3-04 前台 listing 分站价测试：listing 与 checkout 共用同一 ResolveUnitPrice
// （1.x 铁律——分站价只在一处计算；分站域名访问列表/详情/SKU 全部分站价）。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/reseller"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	_ "modernc.org/sqlite"
)

func newCatalogResellerData(t *testing.T) *data.Data {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:catrstest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
}

// TestStoreListingSubsitePrice listing/详情/SKU 分站价同源。
func TestStoreListingSubsitePrice(t *testing.T) {
	d := newCatalogResellerData(t)
	ctx := context.Background()
	rr := reseller.NewResellerRepo(d)

	// 分站主 user 1 → 过审（默认加价率 10%）
	if _, err := d.Client.User.Create().SetUsername("owner").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	profile, err := rr.Apply(ctx, reseller.ApplyInput{UserID: 1, Reason: "开店"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rr.Review(ctx, profile.ID, true, "", 99, 10, 50, 7); err != nil {
		t.Fatal(err)
	}
	subsite := profile.ID

	// 分站自营商品（基础价 1000）+ 主站商品（1000）
	prod, err := rr.CreateOwnProduct(ctx, subsite, reseller.OwnProductInput{Name: "分站自营卡", Price: 1000, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("主站商品").SetSlug("main-prod").
		SetPrice(1000).SetStockType("card").SetStatus(1).Save(ctx); err != nil {
		t.Fatal(err)
	}

	svc := NewStoreCatalogService(NewCatalogUsecase(NewProductRepoImpl(d, nil)), rr)

	// 分站域名上下文 → 列表分站价 +10%
	subsiteCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: subsite, IsMain: false})
	reply, err := svc.ListProducts(subsiteCtx, &storefrontv1.ListProductsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Items) != 1 || reply.Items[0].PriceCents != 1100 {
		t.Fatalf("分站列表价错误: %+v", reply.Items)
	}

	// 详情 + SKU 同源（SKU 规则 > 商品规则）
	if _, err := d.Client.ProductSku.Create().
		SetProductID(prod.ID).SetName("大卡").SetPrice(2000).
		SetSpecValues(map[string]string{"容量": "大"}).Save(ctx); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetProduct(subsiteCtx, &storefrontv1.GetProductRequest{Id: prod.ID})
	if err != nil {
		t.Fatal(err)
	}
	if detail.PriceCents != 1100 {
		t.Fatalf("详情分站价错误: %d", detail.PriceCents)
	}
	if len(detail.Skus) != 1 || detail.Skus[0].PriceCents != 2200 {
		t.Fatalf("SKU 分站价错误（应加价 10%%）: %+v", detail.Skus)
	}

	// 主站上下文 → 主站价 1000（分站商品不可见）
	mainReply, err := svc.ListProducts(ctx, &storefrontv1.ListProductsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mainReply.Items) != 1 || mainReply.Items[0].PriceCents != 1000 {
		t.Fatalf("主站列表应只见主站商品原价: %+v", mainReply.Items)
	}
}

// TestTenantIsolationMatrix 三租户隔离矩阵（R6 必测）：
// 两分站+主站 × 列表/详情/更新/删除/同 slug 全组合互不可见。
func TestTenantIsolationMatrix(t *testing.T) {
	d := newCatalogResellerData(t)
	ctx := context.Background()
	rr := reseller.NewResellerRepo(d)

	// 两个已过审分站（站主 user 1 / user 2）
	seedOwner := func(uid uint64, name string) uint64 {
		if _, err := d.Client.User.Create().SetUsername(name).SetStatus(user.StatusActive).Save(ctx); err != nil {
			t.Fatal(err)
		}
		app, err := rr.Apply(ctx, reseller.ApplyInput{UserID: uid, Reason: "开店"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = rr.Review(ctx, app.ID, true, "", 99, 10, 50, 7); err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	subA := seedOwner(1, "ownerA")
	subB := seedOwner(2, "ownerB")

	// 三租户各建 1 个同 slug 商品（唯一索引含 subsite_id：分站间同 slug 不冲突）
	mk := func(subsite uint64, name string) *ent.Product {
		p, err := rr.CreateOwnProduct(ctx, subsite, reseller.OwnProductInput{Name: name, Price: 1000, Status: 1})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// 英文名 → 同 slug：唯一索引含 subsite_id，分站间同 slug 共存不冲突
	prodA := mk(subA, "SameProduct")
	prodB := mk(subB, "SameProduct")
	prodMain, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("SameProduct").SetSlug("sameproduct-3").
		SetPrice(1000).SetStockType("card").SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = prodMain
	if prodA.Slug != "sameproduct" || prodB.Slug != "sameproduct" {
		t.Fatalf("分站间应允许同 slug 共存: %s vs %s", prodA.Slug, prodB.Slug)
	}

	svc := NewStoreCatalogService(NewCatalogUsecase(NewProductRepoImpl(d, nil)), rr)
	ctxA := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: subA, IsMain: false})
	ctxB := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: subB, IsMain: false})

	// 列表互不可见（各租户只见自己 1 件）
	for name, c := range map[string]context.Context{"A": ctxA, "B": ctxB, "主站": ctx} {
		reply, err := svc.ListProducts(c, &storefrontv1.ListProductsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(reply.Items) != 1 {
			t.Fatalf("%s 租户列表应只见自己 1 件: %d", name, len(reply.Items))
		}
	}

	// 跨租户详情 404（A 的 ID 在 B 上下文不可见）
	if _, err := svc.GetProduct(ctxB, &storefrontv1.GetProductRequest{Id: prodA.ID}); err == nil {
		t.Fatal("跨租户详情应 404")
	}
	if _, err := svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: prodA.ID}); err == nil {
		t.Fatal("主站上下文不应看到分站商品")
	}

	// 更新隔离：A 改名/改价不影响 B 与主站
	adminSvc := NewAdminCatalogService(NewProductRepoImpl(d, nil))
	if _, err := adminSvc.UpdateProduct(ctxA, &adminv1.UpdateProductRequest{
		Id: prodA.ID, Name: "A站改名", PriceCents: 2000,
	}); err != nil {
		t.Fatal(err)
	}
	gotB, _ := d.Client.Product.Get(ctx, prodB.ID)
	if gotB.Name == "A站改名" || gotB.Price == 2000 {
		t.Fatal("A 站更新污染了 B 站商品")
	}
	gotMain, _ := d.Client.Product.Get(ctx, prodMain.ID)
	if gotMain.Name == "A站改名" {
		t.Fatal("A 站更新污染了主站商品")
	}

	// 删除隔离：B 删自己的商品，A 不受影响
	if _, err := adminSvc.DeleteProduct(ctxB, &adminv1.DeleteProductRequest{Id: prodB.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Product.Get(ctx, prodA.ID); err != nil {
		t.Fatal("B 删除影响了 A 的商品")
	}
}
