package order

// P3-04 验收旅程（出单段）端到端测试：
//   分站自营商品上架（分站主 API）→ 分站域名上下文下单 → 管线步骤 7 接
//   ResolveUnitPrice（分站价）→ 快照（subsite_profit/profit_eligible/subsite_domain）
//   → 支付发布 order.paid（载荷带分站快照）→ SettleService 利润入账（幂等）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	inventoryport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/reseller"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	_ "modernc.org/sqlite"
)

// fakeInventory 空库存实现（本测试只验证价格管线与分账链路，不验证锁卡 CAS）。
type fakeInventory struct{}

func (fakeInventory) Reserve(ctx context.Context, subsiteID uint64, items []inventoryport.ReserveItem) (*inventoryport.Reservation, error) {
	return &inventoryport.Reservation{}, nil
}
func (fakeInventory) Release(ctx context.Context, orderID uint64) error { return nil }
func (fakeInventory) BindOrder(ctx context.Context, subsiteID, productID, orderID uint64, quantity int32) error {
	return nil
}
func (fakeInventory) MarkUsed(ctx context.Context, cardIDs []uint64, orderID uint64) error {
	return nil
}
func (fakeInventory) Stock(ctx context.Context, productID, skuID uint64) (int64, error) {
	return 0, nil
}

// captureWriter 捕获 outbox 事件（验证 order.paid 载荷契约）。
type captureWriter struct{ evts []capturedEvent }

type capturedEvent struct {
	typ     string
	payload json.RawMessage
}

func (w *captureWriter) Write(ctx context.Context, module, typ, aggregateID, dedupeKey string, payload json.RawMessage) error {
	w.evts = append(w.evts, capturedEvent{typ: typ, payload: payload})
	return nil
}

func (w *captureWriter) last() capturedEvent { return w.evts[len(w.evts)-1] }

// newJourney 旅程测试环境：内存 SQLite + 已过审分站（站主 user 1）+ 上架商品。
func newJourney(t *testing.T) (*data.Data, *id.Generator, *reseller.ResellerRepo, uint64, *ent.Product) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:orderrsetest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// 站主 user 1 + 过审（默认加价率 10%）
	if _, err := client.User.Create().SetUsername("owner").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	rr := reseller.NewResellerRepo(d)
	profile, err := rr.Apply(ctx, reseller.ApplyInput{UserID: 1, Reason: "开店"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rr.Review(ctx, profile.ID, true, "", 99, 10, 50, 7); err != nil {
		t.Fatal(err)
	}
	subsite := profile.ID
	// 买家 user 2
	if _, err := client.User.Create().SetUsername("buyer").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	// 分站自营商品上架（price 1000，status 上架）
	prod, err := rr.CreateOwnProduct(ctx, subsite, reseller.OwnProductInput{Name: "分站自营卡", Price: 1000, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	return d, gen, rr, subsite, prod
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestResellerOrderJourney 出单段验收旅程全链路。
func TestResellerOrderJourney(t *testing.T) {
	d, gen, rr, subsite, prod := newJourney(t)
	ctx := context.Background()
	// 分站域名访问上下文（tenantFilter 注入形态）
	subsiteCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: subsite, IsMain: false, Host: "shop.example.com"})

	writer := &captureWriter{}
	uc := &OrderUsecase{Data: d, Inv: fakeInventory{}, Gen: gen, Outbox: writer, Reseller: rr}

	// 1) 分站价出单：默认加价率 +10% → 单价 1100 × 2 = 2200
	res, err := uc.CreateOrder(subsiteCtx, CreateOrderInput{
		SubsiteID: subsite, UserID: 2,
		Items: []OrderItemInput{{ProductID: prod.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCents != 2200 {
		t.Fatalf("分站价错误: %d (want 2200)", res.TotalCents)
	}
	o, err := d.Client.Order.Query().Where(order.OrderNo(res.OrderNo)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.SubsiteProfit != 200 || !o.ProfitEligible || o.SubsiteDomain != "shop.example.com" {
		t.Fatalf("分站快照错误: profit=%d eligible=%v domain=%q", o.SubsiteProfit, o.ProfitEligible, o.SubsiteDomain)
	}
	lines, _ := d.Client.OrderAmountLine.Query().Where(orderamountline.OrderID(o.ID)).All(ctx)
	var markupLine *ent.OrderAmountLine
	for _, l := range lines {
		if string(l.Type) == "subsite_markup" {
			markupLine = l
		}
	}
	if markupLine == nil || markupLine.Amount != 100 {
		t.Fatalf("管线步骤 7 金额行缺失或金额错误: %+v", markupLine)
	}

	// 2) 防自购：站主自购 → profit_eligible=false（快照仍记加价基数）
	res2, err := uc.CreateOrder(subsiteCtx, CreateOrderInput{
		SubsiteID: subsite, UserID: 1,
		Items: []OrderItemInput{{ProductID: prod.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	o2, _ := d.Client.Order.Query().Where(order.OrderNo(res2.OrderNo)).Only(ctx)
	if o2.ProfitEligible {
		t.Fatal("站主自购应 profit_eligible=false")
	}
	if o2.SubsiteProfit != 100 {
		t.Fatalf("自购单仍应快照加价基数: %d", o2.SubsiteProfit)
	}

	// 3) 支付 → order.paid 载荷携带分站快照
	if err := uc.MarkPaid(ctx, res.OrderNo); err != nil {
		t.Fatal(err)
	}
	evt := writer.last()
	if evt.typ != events.OrderPaid {
		t.Fatalf("应发布 order.paid: %s", evt.typ)
	}
	var payload struct {
		OrderID        uint64 `json:"order_id"`
		SubsiteID      uint64 `json:"subsite_id"`
		SubsiteProfit  int64  `json:"subsite_profit"`
		ProfitEligible bool   `json:"profit_eligible"`
	}
	if err := json.Unmarshal(evt.payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SubsiteProfit != 200 || !payload.ProfitEligible || payload.SubsiteID != subsite {
		t.Fatalf("order.paid 载荷快照错误: %+v", payload)
	}

	// 4) 利润入账（幂等重放只入一次）
	svc := reseller.NewSettleService(rr, discardLogger())
	env := events.Envelope{Type: events.OrderPaid, Payload: evt.payload}
	if err := svc.OnOrderPaid(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnOrderPaid(ctx, env); err != nil {
		t.Fatal(err)
	}
	rows, total, _ := rr.Ledger(ctx, subsite, "", 1, 10)
	if total != 1 {
		t.Fatalf("分账幂等失败: %d", total)
	}
	if rows[0].Amount != 200 || string(rows[0].Status) != "pending" {
		t.Fatalf("账本行错误: amount=%d status=%s", rows[0].Amount, rows[0].Status)
	}
	acc, _ := rr.GetBalance(ctx, subsite)
	if acc.Available != 200 {
		t.Fatalf("余额缓存错误: %d", acc.Available)
	}

	// 5) 定价规则覆盖：fixed_price 1500 → 加价 500 × 2 → 入账 1000
	if _, err := rr.UpsertPricing(ctx, subsite, prod.ID, 0, "fixed_price", 1500, 50); err != nil {
		t.Fatal(err)
	}
	res3, err := uc.CreateOrder(subsiteCtx, CreateOrderInput{
		SubsiteID: subsite, UserID: 2,
		Items: []OrderItemInput{{ProductID: prod.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res3.TotalCents != 3000 {
		t.Fatalf("fixed_price 分站价错误: %d", res3.TotalCents)
	}
	if err := uc.MarkPaid(ctx, res3.OrderNo); err != nil {
		t.Fatal(err)
	}
	_ = svc.OnOrderPaid(ctx, events.Envelope{Type: events.OrderPaid, Payload: writer.last().payload})
	acc, _ = rr.GetBalance(ctx, subsite)
	if acc.Available != 1200 {
		t.Fatalf("累计余额错误: %d (want 1200)", acc.Available)
	}

	// 6) 自购单支付 → 利润不入账
	if err := uc.MarkPaid(ctx, res2.OrderNo); err != nil {
		t.Fatal(err)
	}
	_ = svc.OnOrderPaid(ctx, events.Envelope{Type: events.OrderPaid, Payload: writer.last().payload})
	acc, _ = rr.GetBalance(ctx, subsite)
	if acc.Available != 1200 {
		t.Fatalf("自购单不应入账: %d", acc.Available)
	}
}

// TestMainSiteOrderUnaffected 主站下单不受分站管线影响（Reseller 装配但 subsite=0）。
func TestMainSiteOrderUnaffected(t *testing.T) {
	d, gen, rr, _, _ := newJourney(t)
	ctx := context.Background()
	// 主站商品（subsite 0）
	prod, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("主站商品").SetSlug("main-prod").
		SetPrice(1000).SetStockType("card").SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uc := &OrderUsecase{Data: d, Inv: fakeInventory{}, Gen: gen, Reseller: rr}
	res, err := uc.CreateOrder(ctx, CreateOrderInput{
		SubsiteID: 0, UserID: 2,
		Items: []OrderItemInput{{ProductID: prod.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCents != 1000 {
		t.Fatalf("主站价不应被分站管线改动: %d", res.TotalCents)
	}
	o, _ := d.Client.Order.Query().Where(order.OrderNo(res.OrderNo)).Only(ctx)
	if o.SubsiteProfit != 0 || !o.ProfitEligible {
		t.Fatalf("主站单快照应为中性: profit=%d eligible=%v", o.SubsiteProfit, o.ProfitEligible)
	}
}
