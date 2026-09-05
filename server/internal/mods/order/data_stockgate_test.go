package order

// 上游库存预检闸门接线测试：闸门拒单（不残留订单）、放行继续建单、
// nil 闸门跳过（预检为可空依赖）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
)

// fakeStockGate 预检闸门桩（记录入参；返回预置结果）。
type fakeStockGate struct {
	err   error
	calls int
	items []orderport.UpstreamStockItem
}

func (f *fakeStockGate) CheckItems(ctx context.Context, subsiteID uint64, items []orderport.UpstreamStockItem) error {
	f.calls++
	f.items = items
	return f.err
}

func newGateEnv(t *testing.T) (*data.Data, *OrderUsecase, uint64) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:ordgatetest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
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
	// 上游代发商品（跳过本地锁卡——预检是其唯一库存防线）
	p, err := client.Product.Create().
		SetSubsiteID(0).SetName("上游商品").SetSlug("gate-1").
		SetPrice(1000).SetStockType("card").
		SetUpstreamSourceID(9).SetUpstreamProductCode("UP-1").
		SetStatus(1).Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	uc := &OrderUsecase{Data: d, Inv: fakeInventory{}, Gen: gen, Outbox: &captureWriter{}}
	return d, uc, p.ID
}

func TestStockGateReject(t *testing.T) {
	d, uc, pid := newGateEnv(t)
	gate := &fakeStockGate{err: errors.New("supply: 商品「上游商品」上游库存不足（余 0 需 1）")}
	uc.SetStockGate(gate)

	_, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		QueryPassword: "test1234",
		Contact:       "buyer@test.com",
		Items:         []OrderItemInput{{ProductID: pid, Quantity: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "order.INSUFFICIENT_STOCK") {
		t.Fatalf("应拒单 INSUFFICIENT_STOCK: %v", err)
	}
	if gate.calls != 1 || len(gate.items) != 1 || gate.items[0].ProductID != pid {
		t.Fatalf("闸门入参错误: calls=%d items=%+v", gate.calls, gate.items)
	}
	n, _ := d.Client.Order.Query().Count(context.Background())
	if n != 0 {
		t.Fatalf("拒单不应残留订单: %d", n)
	}
}

func TestStockGatePass(t *testing.T) {
	_, uc, pid := newGateEnv(t)
	gate := &fakeStockGate{}
	uc.SetStockGate(gate)

	res, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		QueryPassword: "test1234",
		Contact:       "buyer@test.com",
		Items:         []OrderItemInput{{ProductID: pid, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.OrderNo == "" {
		t.Fatal("放行应建单")
	}
	if len(gate.items) != 1 || gate.items[0].Quantity != 2 {
		t.Fatalf("闸门行错误: %+v", gate.items)
	}
}

func TestStockGateNilSkip(t *testing.T) {
	_, uc, pid := newGateEnv(t)
	if _, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		QueryPassword: "test1234",
		Contact:       "buyer@test.com",
		Items:         []OrderItemInput{{ProductID: pid, Quantity: 1}},
	}); err != nil {
		t.Fatalf("nil 闸门应跳过预检: %v", err)
	}
}
