package order

import (
	"context"
	"errors"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	orderent "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	couponmod "github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	couponport "github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	paymentmod "github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"testing"
	"time"
)

func flashPaidEnv(t *testing.T, limit int32) (*data.Data, *OrderUsecase, *couponmod.CouponRepoImpl, *ent.FlashSale) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	repo := couponmod.NewCouponRepoImpl(d)
	uc.Flash = repo
	fs, err := repo.CreateFlash(ctx, 1, 0, 100, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour), limit, 100)
	if err != nil {
		t.Fatal(err)
	}
	return d, uc, repo, fs
}
func flashCreate(t *testing.T, uc *OrderUsecase, key string) *CreateOrderResult {
	t.Helper()
	got, err := uc.CreateOrder(context.Background(), CreateOrderInput{UserID: 7, QueryPassword: "abcd", IdempotencyKey: key, Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func flashCounts(t *testing.T, d *data.Data, id uint64, sold, reserved int32) {
	t.Helper()
	f := d.Client.FlashSale.GetX(context.Background(), id)
	if f.SoldQty != sold || f.ReservedQty != reserved {
		t.Fatalf("sold/reserved=%d/%d want %d/%d", f.SoldQty, f.ReservedQty, sold, reserved)
	}
}

func TestFlashPaidOnlyAndReservationLifecycle(t *testing.T) {
	ctx := context.Background()
	d, uc, repo, fs := flashPaidEnv(t, 1)
	first := flashCreate(t, uc, "same")
	flashCounts(t, d, fs.ID, 0, 1)
	if again := flashCreate(t, uc, "same"); again.OrderNo != first.OrderNo {
		t.Fatal("duplicate order")
	}
	flashCounts(t, d, fs.ID, 0, 1)
	offer, err := repo.Active(ctx, 1, 0)
	if err != nil || offer.Remaining != 1 {
		t.Fatal("unpaid reduced display quantity", err)
	}
	_, err = uc.CreateOrder(ctx, CreateOrderInput{UserID: 8, QueryPassword: "abcd", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if !errors.Is(err, couponport.ErrFlashReserved) {
		t.Fatal("last reservation not protected", err)
	}
	if err = repo.DeleteFlash(ctx, fs.ID); err == nil {
		t.Fatal("deleted reserved campaign")
	}
	uc.Inv = releaseFailure{}
	if err = uc.CancelOrder(ctx, first.OrderNo, "cancel", "system", 0); err == nil {
		t.Fatal("release failure ignored")
	}
	flashCounts(t, d, fs.ID, 0, 1)
	uc.Inv = fakeInventory{}
	for i := 0; i < 2; i++ {
		if err = uc.CancelOrder(ctx, first.OrderNo, "cancel", "system", 0); err != nil {
			t.Fatal(err)
		}
	}
	flashCounts(t, d, fs.ID, 0, 0)
	second := flashCreate(t, uc, "expires")
	o := d.Client.Order.Query().Where(orderent.OrderNo(second.OrderNo)).OnlyX(ctx)
	d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	if n, err := uc.ExpireOrder(ctx); err != nil || n != 1 {
		t.Fatal("expiry failed", n, err)
	}
	flashCounts(t, d, fs.ID, 0, 0)
	third := flashCreate(t, uc, "paid")
	d.Client.FlashSale.UpdateOneID(fs.ID).SetEndAt(time.Now().UTC().Add(-time.Minute)).SaveX(ctx)
	// 已下单获得的秒杀价格和预占在活动结束后仍可于订单支付期内结算。
	for i := 0; i < 2; i++ {
		if err = uc.MarkPaid(ctx, third.OrderNo); err != nil {
			t.Fatal(err)
		}
	}
	flashCounts(t, d, fs.ID, 1, 0)
	if err = uc.CancelOrder(ctx, third.OrderNo, "cancel paid", "system", 0); err == nil {
		t.Fatal("canceled paid order")
	}
	flashCounts(t, d, fs.ID, 1, 0)
}

func TestFlashPaymentTransactionRollbackAndBalanceCallback(t *testing.T) {
	ctx := context.Background()
	d, uc, _, fs := flashPaidEnv(t, 3)
	result := flashCreate(t, uc, "callback")
	o := d.Client.Order.Query().Where(orderent.OrderNo(result.OrderNo)).OnlyX(ctx)
	w := wallet.NewWalletRepoImpl(d)
	d.Client.PaymentChannel.Create().SetName("balance").SetCode("balance").SetDriver("wallet").SetConfig([]byte("{}")).SetEnabled(true).SaveX(ctx)
	pay := paymentmod.NewPaymentRepoImpl(d, nil, paymentmod.NewRegistry(), ProvideOrderLifecycle(uc), wallet.ProvidePortWallet(w), nil, nil, nil, nil, nil)
	payment := d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("balance").SetAmount(result.TotalCents).SetStatus("pending").SaveX(ctx)
	fact := paymentmod.CallbackFact{Channel: "balance", ChannelOrderNo: "test-paid", OrderNo: o.OrderNo, Amount: result.TotalCents, Currency: "CNY", Success: true}
	if err := pay.HandleCallback(ctx, payment.ID, fact); err == nil {
		t.Fatal("insufficient wallet paid")
	}
	flashCounts(t, d, fs.ID, 0, 1)
	if err := w.CreditInTx(ctx, wallet.Entry{UserID: 7, Direction: "in", Type: "adjust", Amount: 1000, Reference: "seed"}); err != nil {
		t.Fatal(err)
	}
	// 整个外层支付事务失败时，秒杀计数、余额和订单状态一起回滚。
	err := data.Tx(ctx, d, func(tx context.Context) error {
		if err := pay.HandleCallback(tx, payment.ID, fact); err != nil {
			return err
		}
		return errors.New("after settlement failed")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	flashCounts(t, d, fs.ID, 0, 1)
	avail, _, _ := w.GetBalance(ctx, 7)
	if avail != 1000 || d.Client.Order.GetX(ctx, o.ID).Status != "pending_payment" {
		t.Fatal("partial financial state committed")
	}
	for i := 0; i < 2; i++ {
		if err := pay.HandleCallback(ctx, payment.ID, fact); err != nil {
			t.Fatal(err)
		}
	}
	flashCounts(t, d, fs.ID, 1, 0)
	avail, _, _ = w.GetBalance(ctx, 7)
	if avail != 1000-result.TotalCents {
		t.Fatal("balance double debit")
	}
}

type flashBindFailure struct{ fakeInventory }

func (flashBindFailure) BindOrder(context.Context, uint64, uint64, uint64, int32) error {
	return errors.New("bind failed after quota reservation")
}

func TestFlashFailedOrderAndLegacyOrderDoNotLeakQuota(t *testing.T) {
	ctx := context.Background()
	d, uc, _, fs := flashPaidEnv(t, 3)
	uc.Inv = flashBindFailure{}
	_, err := uc.CreateOrder(ctx, CreateOrderInput{UserID: 7, QueryPassword: "abcd", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if err == nil {
		t.Fatal("bind failure accepted")
	}
	flashCounts(t, d, fs.ID, 0, 0)
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("failed order persisted")
	}
	uc.Inv = fakeInventory{}
	d.Client.FlashSale.UpdateOneID(fs.ID).SetSoldQty(1).SaveX(ctx)
	old := d.Client.Order.Create().SetOrderNo("legacy-paid-on-create").SetStatus("pending_payment").SetTotalAmount(100).SaveX(ctx)
	if err := uc.MarkPaid(ctx, old.OrderNo); err != nil {
		t.Fatal(err)
	}
	flashCounts(t, d, fs.ID, 1, 0)
	result := flashCreate(t, uc, "bad-snapshot")
	o := d.Client.Order.Query().Where(orderent.OrderNo(result.OrderNo)).OnlyX(ctx)
	d.Client.Order.UpdateOneID(o.ID).SetExtra(map[string]any{"flash_reservations": []map[string]any{{"id": "invalid", "quantity": 1}}}).SaveX(ctx)
	if err := uc.MarkPaid(ctx, result.OrderNo); err == nil {
		t.Fatal("invalid snapshot paid")
	}
	flashCounts(t, d, fs.ID, 1, 1)
	if d.Client.Order.GetX(ctx, o.ID).Status != "pending_payment" {
		t.Fatal("status changed despite failed quota")
	}
}
