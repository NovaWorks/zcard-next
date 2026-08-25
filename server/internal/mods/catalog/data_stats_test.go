package catalog

// P1-01 管理列表库存/已售填充测试：StockBatch（cards available 计数，链接类不入卡池）
// + SoldBatch（paid+ 订单 quantity 聚合，pending/canceled 不计）+ fillStats 降级口径。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	_ "modernc.org/sqlite"
)

func newStatsEnv(t *testing.T) (*data.Data, *AdminCatalogService) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:catstats%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	gen, _ := id.NewGenerator(1)
	cardRepo := inventory.NewCardRepoImpl(d, nil)
	uc := &ordermod.OrderUsecase{Data: d, Gen: gen}
	svc := NewAdminCatalogService(NewProductRepoImpl(d, nil), cardRepo, uc, nil, nil)
	return d, svc
}

// TestListProductsStats 列表填充：卡密类=available 计数；链接类=-1 不限；已售=paid+ 聚合。
func TestListProductsStats(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()

	// 商品 A（卡密类）+ B（链接类）
	a, err := d.Client.Product.Create().SetSubsiteID(0).SetName("A").SetSlug("a").
		SetPrice(1000).SetStockType("card").SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Client.Product.Create().SetSubsiteID(0).SetName("B").SetSlug("b").
		SetPrice(2000).SetStockType("url").SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A 的卡密：2 可用 + 1 已售 + 1 锁定 → 库存 = 2
	for i, st := range []card.Status{card.StatusAvailable, card.StatusAvailable, card.StatusUsed, card.StatusReserved} {
		if _, err := d.Client.Card.Create().
			SetProductID(a.ID).SetStatus(st).
			SetContent([]byte(fmt.Sprintf("c%d", i))).SetContentHash(fmt.Sprintf("h%d", i)).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 订单：paid 买 A×3；pending 买 A×5（不计）；canceled 买 B×7（不计）→ A 已售 3、B 0
	mkOrder := func(status order.Status, productID uint64, qty int32) {
		o, err := d.Client.Order.Create().
			SetOrderNo(fmt.Sprintf("S-%d-%d", productID, qty)).SetSubsiteID(0).
			SetStatus(status).SetTotalAmount(0).SetBaseCurrency("CNY").SetVersion(0).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Client.OrderItem.Create().
			SetOrderID(o.ID).SetProductID(productID).SetUnitPrice(0).
			SetQuantity(qty).SetAmount(0).SetFulfillmentType(orderitem.FulfillmentTypeAuto).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mkOrder(order.StatusPaid, a.ID, 3)
	mkOrder(order.StatusPendingPayment, a.ID, 5)
	mkOrder(order.StatusCanceled, b.ID, 7)

	reply, err := svc.ListProducts(ctx, &adminv1.ListProductsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uint64]*adminv1.AdminProduct{}
	for _, p := range reply.Products {
		byID[p.Id] = p
	}
	if byID[a.ID].Stock != 2 {
		t.Fatalf("A 库存错误: %d (want 2)", byID[a.ID].Stock)
	}
	if byID[a.ID].SoldCount != 3 {
		t.Fatalf("A 已售错误: %d (want 3)", byID[a.ID].SoldCount)
	}
	if byID[b.ID].Stock != -1 {
		t.Fatalf("B 链接类库存应 -1 不限: %d", byID[b.ID].Stock)
	}
	if byID[b.ID].SoldCount != 0 {
		t.Fatalf("B canceled 不计已售: %d", byID[b.ID].SoldCount)
	}
}
