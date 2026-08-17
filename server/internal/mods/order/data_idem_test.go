package order

// P1-03 补全测试（2026-08-17）：Idempotency-Key 下单幂等、慢通道顺延不误杀、
// 我的订单列表、登录态取货免密码、用户取消。

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
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	paymentmod "github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	_ "modernc.org/sqlite"
)

func newIdemEnv(t *testing.T) (*data.Data, *OrderUsecase, *paymentmod.PaymentRepoImpl) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:orderidemtest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
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
	if _, err := client.Product.Create().
		SetSubsiteID(0).SetName("幂等商品").SetSlug("idem-1").
		SetPrice(500).SetStockType("card").SetStatus(1).Save(ctx); err != nil {
		t.Fatal(err)
	}
	uc := &OrderUsecase{Data: d, Inv: fakeInventory{}, Gen: gen}
	// 慢通道探测只需 data 句柄，其余依赖 nil（HasPendingSlowPayment 不触达）
	payRepo := paymentmod.NewPaymentRepoImpl(d, nil, nil, nil, nil, nil, nil, nil)
	return d, uc, payRepo
}

// TestCreateOrderIdempotency 同 Idempotency-Key 双击只产生一单，返回首单。
func TestCreateOrderIdempotency(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	in := CreateOrderInput{
		UserID:         3,
		Items:          []OrderItemInput{{ProductID: 1, Quantity: 1}},
		IdempotencyKey: "dup-key-001",
	}
	first, err := uc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := uc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OrderNo != second.OrderNo || first.TotalCents != second.TotalCents {
		t.Fatalf("幂等应返回首单: first=%+v second=%+v", first, second)
	}
	n, _ := d.Client.Order.Query().Count(ctx)
	if n != 1 {
		t.Fatalf("同 key 重复下单产生多单: %d", n)
	}
	// 不同 key 正常新单
	other, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 3, Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}, IdempotencyKey: "dup-key-002",
	})
	if err != nil {
		t.Fatal(err)
	}
	if other.OrderNo == first.OrderNo {
		t.Fatal("不同 key 不应复用订单")
	}
	n, _ = d.Client.Order.Query().Count(ctx)
	if n != 2 {
		t.Fatalf("不同 key 应新单: %d", n)
	}
}

// TestExpireSlowChannelDeferral 慢通道顺延：epusdt pending 流水不误杀，普通单正常取消。
func TestExpireSlowChannelDeferral(t *testing.T) {
	d, uc, payRepo := newIdemEnv(t)
	uc.SetSlowPaymentChecker(payRepo)
	ctx := context.Background()
	past := time.Now().UTC().Add(-2 * time.Hour)

	// 单 A：epusdt 慢通道 pending 流水（应顺延）；单 B：无流水（应取消）
	a, err := uc.CreateOrder(ctx, CreateOrderInput{UserID: 3, Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := uc.CreateOrder(ctx, CreateOrderInput{UserID: 3, Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	oa, _ := d.Client.Order.Query().Where(order.OrderNo(a.OrderNo)).Only(ctx)
	ob, _ := d.Client.Order.Query().Where(order.OrderNo(b.OrderNo)).Only(ctx)

	if _, err := d.Client.PaymentChannel.Create().
		SetName("USDT").SetCode("epusdt1").SetDriver("epusdt").
		SetConfig([]byte("{}")).SetEnabled(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Payment.Create().
		SetOrderID(oa.ID).SetChannel("epusdt1").SetAmount(500).
		SetStatus(payment.StatusPending).Save(ctx); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Client.Order.UpdateOneID(oa.ID).SetExpiredAt(past).Save(ctx)
	_, _ = d.Client.Order.UpdateOneID(ob.ID).SetExpiredAt(past).Save(ctx)

	if _, err := uc.ExpireOrder(ctx); err != nil {
		t.Fatal(err)
	}
	ga, _ := d.Client.Order.Get(ctx, oa.ID)
	gb, _ := d.Client.Order.Get(ctx, ob.ID)
	if ga.Status == order.StatusCanceled || ga.Status == order.StatusExpired {
		t.Fatalf("慢通道单被误杀: %s", ga.Status)
	}
	if !ga.ExpiredAt.After(time.Now().UTC()) {
		t.Fatal("慢通道单应顺延 expired_at")
	}
	if gb.Status != order.StatusCanceled && gb.Status != order.StatusExpired {
		t.Fatalf("普通超时单未取消: %s", gb.Status)
	}
}

// TestMyOrdersAndOwnerFetch 我的订单列表 + 登录态本人取货免密码 + 用户取消。
func TestMyOrdersAndOwnerFetch(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	svc := NewStoreOrderService(uc)
	ctx := context.Background()

	// 本人单（带查询密码）+ 他人单
	mine, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 3, QueryPassword: "secret",
		Items: []OrderItemInput{{ProductID: 1, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 4, QueryPassword: "other",
		Items: []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err != nil {
		t.Fatal(err)
	}

	ownerCtx := identity.WithClaims(ctx, &authn.Claims{Subject: 3, Realm: authn.RealmUser})
	otherCtx := identity.WithClaims(ctx, &authn.Claims{Subject: 4, Realm: authn.RealmUser})

	// 我的订单：user 3 只见 1 单
	list, err := svc.ListMyOrders(ownerCtx, &storefrontv1.ListMyOrdersRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Orders) != 1 || list.Orders[0].OrderNo != mine.OrderNo {
		t.Fatalf("我的订单过滤错误: total=%d", list.Total)
	}

	// 登录态本人取货：免查询密码
	got, err := svc.GetOrder(ownerCtx, &storefrontv1.GetOrderRequest{OrderNo: mine.OrderNo})
	if err != nil {
		t.Fatalf("登录态本人取货应免密码: %v", err)
	}
	if got.OrderNo != mine.OrderNo {
		t.Fatal("取货单号错误")
	}
	// 非本人登录态：不泄露存在性（未带密码 → NOT_FOUND）
	if _, err := svc.GetOrder(otherCtx, &storefrontv1.GetOrderRequest{OrderNo: mine.OrderNo}); err == nil {
		t.Fatal("非本人登录态不应免密码取货")
	}

	// 用户取消：本人 pending 单可取消
	if _, err := svc.CancelMyOrder(ownerCtx, &storefrontv1.CancelMyOrderRequest{OrderNo: mine.OrderNo}); err != nil {
		t.Fatalf("本人取消失败: %v", err)
	}
	o, _ := d.Client.Order.Query().Where(order.OrderNo(mine.OrderNo)).Only(ctx)
	if o.Status != order.StatusCanceled {
		t.Fatalf("取消后状态错误: %s", o.Status)
	}

	// 非本人取消 → NOT_FOUND（不泄露存在性）
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 3,
		Items:  []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	orders3, _, _ := uc.ListUserOrders(ctx, 3, "", 1, 10)
	if len(orders3) == 0 {
		t.Fatal("种子单缺失")
	}
	if _, err := svc.CancelMyOrder(otherCtx, &storefrontv1.CancelMyOrderRequest{OrderNo: orders3[0].OrderNo}); err == nil {
		t.Fatal("非本人取消应拒绝")
	}
}
