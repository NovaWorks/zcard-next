package order

// P3-01 积分兑换下单测试：积分商品全兑（同事务扣分直落 paid + order.paid 发布）、
// 混合购物车拒绝、积分不足整单回滚、游客拒绝。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pointtransaction"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	_ "modernc.org/sqlite"
)

// pointsDebitAdapter 真实积分账本扣减适配（与 wallet providers 同构）。
type pointsDebitAdapter struct{ repo *wallet.WalletRepoImpl }

func (a pointsDebitAdapter) PointDebitInTx(ctx context.Context, e walletport.PointEntry) error {
	return a.repo.PointDebitInTx(ctx, wallet.PointEntry{
		UserID: e.UserID, Direction: e.Direction, Type: e.Type, Amount: e.Amount,
		Reference: e.Reference, OrderID: e.OrderID, Remark: e.Remark,
	})
}

func newPointsEnv(t *testing.T) (*data.Data, *OrderUsecase, *wallet.WalletRepoImpl, *ent.Product, *ent.Product) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:ordpointstest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
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
	wrepo := wallet.NewWalletRepoImpl(d)
	// 买家 user 7：预存 500 积分
	if err := wrepo.PointCreditInTx(ctx, wallet.PointEntry{
		UserID: 7, Direction: "in", Type: "adjust", Amount: 500, Reference: "seed:7",
	}); err != nil {
		t.Fatal(err)
	}
	// 积分商品（100 分/件）+ 常规商品
	pPoints, err := client.Product.Create().
		SetSubsiteID(0).SetName("积分商品").SetSlug("pts-1").
		SetPrice(0).SetStockType("card").SetPointsRequired(100).SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pNormal, err := client.Product.Create().
		SetSubsiteID(0).SetName("常规商品").SetSlug("nrm-1").
		SetPrice(1000).SetStockType("card").SetStatus(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writer := &captureWriter{}
	uc := &OrderUsecase{Data: d, Inv: fakeInventory{}, Gen: gen, Outbox: writer, Points: pointsDebitAdapter{repo: wrepo}}
	return d, uc, wrepo, pPoints, pNormal
}

// TestPointsExchangeOrder 全兑：2×100 分 → 扣 200、余 300、订单直落 paid、事件发布。
func TestPointsExchangeOrder(t *testing.T) {
	d, uc, wrepo, pPoints, _ := newPointsEnv(t)
	ctx := context.Background()

	res, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 7, UsePoints: true,
		Items: []OrderItemInput{{ProductID: pPoints.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCents != 0 {
		t.Fatalf("积分单应付应为 0: %d", res.TotalCents)
	}
	o, err := d.Client.Order.Query().Where(order.OrderNo(res.OrderNo)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != order.StatusPaid {
		t.Fatalf("积分单应直落 paid: %s", o.Status)
	}
	// JSON 读回数值为 float64（方言无关断言）
	if fmt.Sprintf("%.0f", o.Extra["points_total"]) != "200" {
		t.Fatalf("积分快照错误: %+v", o.Extra)
	}
	// 扣分 200 → 余 300；流水 type=redeem
	bal, _ := wrepo.GetPoints(ctx, 7)
	if bal != 300 {
		t.Fatalf("积分扣减错误: %d (want 300)", bal)
	}
	tx, _ := d.Client.PointTransaction.Query().Where(pointtransaction.ReferenceEQ("points_pay:" + res.OrderNo)).All(ctx)
	if len(tx) != 1 || tx[0].Type != "redeem" {
		t.Fatalf("积分流水错误: %+v", tx)
	}
	// order.paid 已发布（载荷 total=0——下游佣金/积分产生自动豁免）
	if len(uc.Outbox.(*captureWriter).evts) == 0 || uc.Outbox.(*captureWriter).last().typ != events.OrderPaid {
		t.Fatalf("order.paid 未发布: %+v", uc.Outbox.(*captureWriter).evts)
	}
}

// TestPointsExchangeReject 混合购物车 / 积分不足 / 游客 拒绝且不残留。
func TestPointsExchangeReject(t *testing.T) {
	d, uc, wrepo, pPoints, pNormal := newPointsEnv(t)
	ctx := context.Background()
	ordersBefore, _ := d.Client.Order.Query().Count(ctx)

	// 混合购物车（常规商品不可积分兑换）
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 7, UsePoints: true,
		Items: []OrderItemInput{{ProductID: pPoints.ID, Quantity: 1}, {ProductID: pNormal.ID, Quantity: 1}},
	}); err == nil || !contains2(err.Error(), "POINTS_MIXED") {
		t.Fatalf("混合购物车应拒绝: %v", err)
	}
	// 积分不足（500 分买 6 件=600 分）→ 整单回滚
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 7, UsePoints: true,
		Items: []OrderItemInput{{ProductID: pPoints.ID, Quantity: 6}},
	}); err == nil || !contains2(err.Error(), "POINTS_INSUFFICIENT") {
		t.Fatalf("积分不足应拒绝: %v", err)
	}
	// 游客
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 0, UsePoints: true,
		Items: []OrderItemInput{{ProductID: pPoints.ID, Quantity: 1}},
	}); err == nil || !contains2(err.Error(), "POINTS_LOGIN") {
		t.Fatalf("游客积分单应拒绝: %v", err)
	}
	// 拒绝后零残留：订单数不变、积分余额不变
	ordersAfter, _ := d.Client.Order.Query().Count(ctx)
	if ordersAfter != ordersBefore {
		t.Fatalf("失败单残留: %d → %d", ordersBefore, ordersAfter)
	}
	bal, _ := wrepo.GetPoints(ctx, 7)
	if bal != 500 {
		t.Fatalf("拒绝后积分被扣: %d", bal)
	}
}

func contains2(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || searchString2(s, sub))
}

func searchString2(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
