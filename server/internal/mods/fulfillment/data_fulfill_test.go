package fulfillment

// 自动交付（M1b 接线）测试：order.paid → FulfillOrder（标记/即删两模式）
// + 消费函数 OnOrderPaid 解析事件载荷。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	_ "modernc.org/sqlite"
)

func newFulfillData(t *testing.T) (*data.Data, *inventory.CardCipher, *DeliveryRepoImpl) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:fulfilltest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewDeliveryRepoImpl(d, cipher, nil, nil)
	return d, cipher, repo
}

// seedPaidOrderWithCards 建商品（指定发货模式）+ N 张加密卡 + 已锁定订单（paid）。
func seedPaidOrderWithCards(t *testing.T, d *data.Data, cipher *inventory.CardCipher, deliveryMode string, count int) (uint64, *ent.Order) {
	t.Helper()
	ctx := context.Background()
	prod, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("卡密商品").SetSlug("card-prod").
		SetPrice(1000).SetStockType(product.StockTypeCard).
		SetDeliveryMode(product.DeliveryMode(deliveryMode)).
		SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cardIDs []uint64
	for i := 0; i < count; i++ {
		plain := fmt.Sprintf("CARD-SECRET-%d", i)
		sealed, err := cipher.Seal(plain, prod.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		c, err := d.Client.Card.Create().
			SetProductID(prod.ID).SetSubsiteID(0).
			SetContent(sealed).
			SetContentHash(cipher.ContentHash(plain)).
			SetStatus(card.StatusReserved).
			SetOrderID(1). // 占位，订单建好后回填
			Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cardIDs = append(cardIDs, c.ID)
	}
	o, err := d.Client.Order.Create().
		SetOrderNo("F10001").SetSubsiteID(0).SetUserID(1).
		SetStatus(order.StatusPaid).SetTotalAmount(int64(count * 1000)).
		SetBaseCurrency("CNY").SetVersion(0).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range cardIDs {
		if _, err := d.Client.Card.UpdateOneID(cid).SetOrderID(o.ID).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Client.OrderItem.Create().
		SetOrderID(o.ID).SetSubsiteID(0).SetProductID(prod.ID).
		SetUnitPrice(1000).SetQuantity(int32(count)).SetAmount(int64(count * 1000)).
		SetFulfillmentType("auto").SetFulfillmentStatus("pending").Save(ctx); err != nil {
		t.Fatal(err)
	}
	return prod.ID, o
}

// TestFulfillStatusMode 标记模式：reserved→used + 交付记录 + 订单 delivered。
func TestFulfillStatusMode(t *testing.T) {
	d, cipher, repo := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 2)

	if err := repo.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Order.Get(ctx, o.ID)
	if string(got.Status) != "delivered" {
		t.Fatalf("订单应 delivered: %s", got.Status)
	}
	// 卡全部 used 且仍存在（标记模式）
	used, _ := d.Client.Card.Query().Where(card.OrderID(o.ID), card.StatusEQ(card.StatusUsed)).Count(ctx)
	if used != 2 {
		t.Fatalf("应有 2 张 used 卡: %d", used)
	}
	// 交付记录 delivered_mode=status
	deliveries, _ := d.Client.OrderDelivery.Query().Where(orderdelivery.OrderID(o.ID)).All(ctx)
	if len(deliveries) != 2 || string(deliveries[0].DeliveredMode) != "status" {
		t.Fatalf("交付记录错误: %+v", deliveries)
	}
	// 幂等：重复交付直接返回
	if err := repo.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
}

// TestFulfillDeleteMode 即删模式：交付后卡密行物理删除 + 取货占位兜底。
func TestFulfillDeleteMode(t *testing.T) {
	d, cipher, repo := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "delete", 2)

	if err := repo.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Order.Get(ctx, o.ID)
	if string(got.Status) != "delivered" {
		t.Fatalf("订单应 delivered: %s", got.Status)
	}
	// 即删：卡密行物理不存在（§5.20.2 断言）
	cnt, _ := d.Client.Card.Query().Where(card.OrderID(o.ID)).Count(ctx)
	if cnt != 0 {
		t.Fatalf("即删模式卡密行应物理删除: %d", cnt)
	}
	// 交付记录 delivered_mode=delete（取货占位已兜底）
	deliveries, _ := d.Client.OrderDelivery.Query().Where(orderdelivery.OrderID(o.ID)).All(ctx)
	if len(deliveries) != 2 || string(deliveries[0].DeliveredMode) != "delete" {
		t.Fatalf("交付记录错误: %+v", deliveries)
	}
}

// TestOnOrderPaidConsumer 消费函数：order.paid 事件载荷 → FulfillOrder。
func TestOnOrderPaidConsumer(t *testing.T) {
	d, cipher, repo := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)

	payload, _ := json.Marshal(map[string]any{"order_no": o.OrderNo, "order_id": o.ID})
	if err := repo.OnOrderPaid(ctx, events.Envelope{Type: events.OrderPaid, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Order.Get(ctx, o.ID)
	if string(got.Status) != "delivered" {
		t.Fatalf("消费后订单应 delivered: %s", got.Status)
	}
	// 空载荷 ACK 不报错
	if err := repo.OnOrderPaid(ctx, events.Envelope{Type: events.OrderPaid, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

var _ = time.Now

// TestFulfillUpstreamPendingNotDelivered 含上游项订单：未到卡不得落 delivered。
// 纯上游 → fulfilling；本地已交+上游在途 → partially_delivered；上游到卡
// （AttachUpstreamDelivery）→ delivered。曾无条件 paid→delivered 致客户/后台
// 均无卡密可看（线上实测症状）。
func TestFulfillUpstreamPendingNotDelivered(t *testing.T) {
	d, cipher, repo := newFulfillData(t)
	ctx := context.Background()

	// 商品 A：本地卡密（2 张 reserved）；商品 B：上游项（无卡池）
	prodA, _ := d.Client.Product.Create().
		SetSubsiteID(0).SetName("本地卡").SetSlug("local-a").
		SetPrice(1000).SetStockType(product.StockTypeCard).
		SetDeliveryMode(product.DeliveryMode("status")).
		SetStatus(1).Save(ctx)
	prodB, _ := d.Client.Product.Create().
		SetSubsiteID(0).SetName("上游品").SetSlug("up-b").
		SetPrice(2000).SetStockType(product.StockTypeCard).
		SetUpstreamSourceID(9).
		SetStatus(1).Save(ctx)

	o, _ := d.Client.Order.Create().
		SetOrderNo("F20001").SetSubsiteID(0).SetUserID(1).
		SetStatus(order.StatusPaid).SetTotalAmount(4000).
		SetBaseCurrency("CNY").SetVersion(0).Save(ctx)
	itA, _ := d.Client.OrderItem.Create().
		SetOrderID(o.ID).SetSubsiteID(0).SetProductID(prodA.ID).
		SetUnitPrice(1000).SetQuantity(2).SetAmount(2000).
		SetFulfillmentType("auto").SetFulfillmentStatus("pending").Save(ctx)
	_ = itA
	itB, _ := d.Client.OrderItem.Create().
		SetOrderID(o.ID).SetSubsiteID(0).SetProductID(prodB.ID).
		SetUnitPrice(2000).SetQuantity(1).SetAmount(2000).
		SetFulfillmentType("upstream").SetFulfillmentStatus("pending").Save(ctx)

	for i := 0; i < 2; i++ {
		plain := fmt.Sprintf("LOCAL-%d", i)
		sealed, err := cipher.Seal(plain, prodA.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Client.Card.Create().
			SetProductID(prodA.ID).SetSubsiteID(0).
			SetContent(sealed).SetContentHash(cipher.ContentHash(plain)).
			SetStatus(card.StatusReserved).SetOrderID(o.ID).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 1) 本地交付 + 上游在途 → partially_delivered（非 delivered）
	if err := repo.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Order.Get(ctx, o.ID)
	if string(got.Status) != "partially_delivered" {
		t.Fatalf("混合单上游未到卡应为 partially_delivered: %s", got.Status)
	}

	// 2) 上游到卡 → delivered
	plain := "UP-SECRET-1"
	sealed, err := cipher.Seal(plain, prodB.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AttachUpstreamDelivery(ctx, o.ID, itB.ID, prodB.ID, []port.UpstreamDeliveryItem{
		{SealedContent: sealed, ContentHash: cipher.ContentHash(plain)},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = d.Client.Order.Get(ctx, o.ID)
	if string(got.Status) != "delivered" {
		t.Fatalf("上游到卡后应 delivered: %s", got.Status)
	}

	// 3) 纯上游单：FulfillOrder 不落 delivered → fulfilling
	o2, _ := d.Client.Order.Create().
		SetOrderNo("F20002").SetSubsiteID(0).SetUserID(1).
		SetStatus(order.StatusPaid).SetTotalAmount(2000).
		SetBaseCurrency("CNY").SetVersion(0).Save(ctx)
	if _, err := d.Client.OrderItem.Create().
		SetOrderID(o2.ID).SetSubsiteID(0).SetProductID(prodB.ID).
		SetUnitPrice(2000).SetQuantity(1).SetAmount(2000).
		SetFulfillmentType("upstream").SetFulfillmentStatus("pending").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.FulfillOrder(ctx, o2.OrderNo); err != nil {
		t.Fatal(err)
	}
	got2, _ := d.Client.Order.Get(ctx, o2.ID)
	if string(got2.Status) != "fulfilling" {
		t.Fatalf("纯上游单未到卡应为 fulfilling: %s", got2.Status)
	}
}
