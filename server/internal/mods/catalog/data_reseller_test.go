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
