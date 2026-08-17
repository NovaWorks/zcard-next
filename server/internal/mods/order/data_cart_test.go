package order

// P1-03b 购物车测试：合并语义（同 user+product+sku）、属主校验（他人条目不可改删）、
// 失效打标（下架商品 valid=false）、改量 0 删除。

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
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newCartEnv(t *testing.T) (*StoreCartService, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:carttest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewStoreCartService(d, nil, nil), d // pricing/inv nil：快照降级路径不炸
}

func userCtx(userID uint64) context.Context {
	return identity.WithClaims(context.Background(), &authn.Claims{Subject: userID})
}

func TestCartAddMerge(t *testing.T) {
	svc, d := newCartEnv(t)
	ctx := userCtx(1)
	// 上架商品
	if _, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("卡A").SetSlug("c-a").
		SetPrice(1000).SetStockType("card").SetStatus(1).Save(ctx); err != nil {
		t.Fatal(err)
	}

	// 首次加购 2
	it, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 2})
	if err != nil {
		t.Fatal(err)
	}
	if it.Quantity != 2 || !it.Valid {
		t.Fatalf("首购异常: %+v", it)
	}
	// 同 (user, product, sku=0) 再加 3 → 合并为 5（非新行）
	it2, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 3})
	if err != nil {
		t.Fatal(err)
	}
	if it2.Id != it.Id || it2.Quantity != 5 {
		t.Fatalf("合并且错误: id %d→%d qty %d", it.Id, it2.Id, it2.Quantity)
	}
	// 不同用户不串车
	if _, err := svc.AddCartItem(userCtx(2), &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	list2, _ := svc.ListCart(userCtx(2), nil)
	if len(list2.Items) != 1 || list2.Items[0].Quantity != 1 {
		t.Fatal("用户 2 应有独立条目")
	}
}

func TestCartOwnershipAndQuantity(t *testing.T) {
	svc, d := newCartEnv(t)
	ctx := userCtx(1)
	if _, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("B").SetSlug("c-b").
		SetPrice(500).SetStockType("card").SetStatus(1).Save(ctx); err != nil {
		t.Fatal(err)
	}
	it, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 他人不可改/删
	other := userCtx(2)
	if _, err := svc.UpdateCartItem(other, &storefrontv1.UpdateCartItemRequest{Id: it.Id, Quantity: 2}); err == nil {
		t.Fatal("他人改量应拒绝")
	}
	if _, err := svc.RemoveCartItem(other, &storefrontv1.RemoveCartItemRequest{Id: it.Id}); err == nil {
		t.Fatal("他人删除应拒绝")
	}
	// 属主改量
	upd, err := svc.UpdateCartItem(ctx, &storefrontv1.UpdateCartItemRequest{Id: it.Id, Quantity: 7})
	if err != nil || upd.Quantity != 7 {
		t.Fatalf("改量失败: %v %+v", err, upd)
	}
	// quantity=0 → 删除
	if _, err := svc.UpdateCartItem(ctx, &storefrontv1.UpdateCartItemRequest{Id: it.Id, Quantity: 0}); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.ListCart(ctx, nil)
	if len(list.Items) != 0 {
		t.Fatal("quantity=0 应删除条目")
	}
	// 非法数量
	if _, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 100}); err == nil {
		t.Fatal("数量>99 应拒绝")
	}
}

func TestCartInvalidFlag(t *testing.T) {
	svc, d := newCartEnv(t)
	ctx := userCtx(1)
	// 下架商品（status=0）
	if _, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("下架品").SetSlug("c-off").
		SetPrice(300).SetStockType("card").SetStatus(0).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListCart(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Valid {
		t.Fatalf("下架商品应 valid=false 打标: %+v", list.Items)
	}
	if list.Total != 0 {
		t.Fatalf("有效计数应为 0: %d", list.Total)
	}
	// 不存在商品加购拒绝
	if _, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: 999, Quantity: 1}); err == nil {
		t.Fatal("幽灵商品应拒绝")
	}
	// 未登录 401
	if _, err := svc.AddCartItem(context.Background(), &storefrontv1.AddCartItemRequest{ProductId: 1, Quantity: 1}); err == nil {
		t.Fatal("未登录应拒绝")
	}
}
