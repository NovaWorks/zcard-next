package order

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"testing"
	"time"
)

func physicalAddress() map[string]string {
	return map[string]string{"country": "US", "region": "CA", "city": "San Francisco", "address": "123 Test Street", "name": "Test Buyer", "phone": "+1 415 555 0100", "postal_code": "94105"}
}
func physicalInput() CreateOrderInput {
	return CreateOrderInput{UserID: 3, QueryPassword: "test1234", ShippingAddress: physicalAddress(), Items: []OrderItemInput{{ProductID: 1, Quantity: 2}}}
}
func quotePhysical(t *testing.T, uc *OrderUsecase, in CreateOrderInput) CreateOrderInput {
	t.Helper()
	q := in
	q.QuoteOnly = true
	r, e := uc.CreateOrder(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	in.QuoteKey = r.QuoteKey
	return in
}
func seedPhysical(t *testing.T) (*data.Data, *OrderUsecase) {
	d, uc, _ := newIdemEnv(t)
	d.Client.Product.UpdateOneID(1).SetGoodsType("physical").SetShippingMode("fixed").SetShippingFee(300).SetShippingCountries([]string{"US"}).SetPhysicalStock(5).ExecX(context.Background())
	uc.Outbox = data.NewOutboxWriter(d)
	return d, uc
}
func TestPhysicalQuoteReservationCancelAndReplay(t *testing.T) {
	ctx := context.Background()
	d, uc := seedPhysical(t)
	in := physicalInput()
	in.IdempotencyKey = "physical-checkout-key"
	in = quotePhysical(t, uc, in)
	if d.Client.Order.Query().CountX(ctx) != 0 || d.Client.PhysicalStockMovement.Query().CountX(ctx) != 0 || d.Client.Product.GetX(ctx, 1).PhysicalStock != 5 {
		t.Fatal("quote mutated commerce state")
	}
	r, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if r.TotalCents != 1300 || r.ShippingCents != 300 {
		t.Fatalf("bad price %+v", r)
	}
	if d.Client.Product.GetX(ctx, 1).PhysicalStock != 3 {
		t.Fatal("reservation missing")
	}
	o := d.Client.Order.Query().OnlyX(ctx)
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	if o.CommerceVersion != 1 || it.PaidAmount != 1000 || it.ShippingAmount != 300 {
		t.Fatal("snapshot missing")
	}
	// Config changes and a consumed coupon/captcha must not make replay create another order.
	d.Client.Product.UpdateOneID(1).SetShippingCountries([]string{"CN"}).SetStatus(0).ExecX(ctx)
	again, e := uc.CreateOrder(ctx, in)
	if e != nil || again.OrderNo != r.OrderNo {
		t.Fatalf("replay %v", e)
	}
	in.Items[0].Quantity = 1
	if _, e = uc.CreateOrder(ctx, in); e == nil {
		t.Fatal("changed retry accepted")
	}
	if e = uc.CancelOrder(ctx, r.OrderNo, "cancel", "user", 3); e != nil {
		t.Fatal(e)
	}
	if e = uc.CancelOrder(ctx, r.OrderNo, "retry", "user", 3); e != nil {
		t.Fatal(e)
	}
	if d.Client.Product.GetX(ctx, 1).PhysicalStock != 5 || d.Client.Order.GetX(ctx, o.ID).ShippingStatus != "canceled" {
		t.Fatal("stock not released exactly once")
	}
}
func TestPhysicalScopedCouponAndSKUShipping(t *testing.T) {
	ctx := context.Background()
	d, uc := seedPhysical(t)
	uc.Catalog = catalog.NewProductRepoImpl(d, nil)
	uc.Coupon = coupon.NewCouponRepoImpl(d)
	a := d.Client.ProductSku.Create().SetProductID(1).SetName("A").SetSpecValues(map[string]string{}).SetPhysicalStock(3).SaveX(ctx)
	b := d.Client.ProductSku.Create().SetProductID(1).SetName("B").SetSpecValues(map[string]string{}).SetPhysicalStock(3).SaveX(ctx)
	v := d.Client.Product.Create().SetName("Digital").SetSlug("digital").SetPrice(1000).SetFulfillmentMode("manual").SetStatus(1).SaveX(ctx)
	d.Client.Coupon.Create().SetName("Physical coupon").SetCode("PHYSICAL").SetType("fixed").SetValue(301).SetStatus("unused").SetScope(map[string]any{"product_ids": []any{float64(1)}}).SaveX(ctx)
	in := physicalInput()
	in.CouponCode = "PHYSICAL"
	in.Items = []OrderItemInput{{ProductID: 1, SkuID: a.ID, Quantity: 1}, {ProductID: 1, SkuID: b.ID, Quantity: 1}, {ProductID: v.ID, Quantity: 1}}
	in = quotePhysical(t, uc, in)
	r, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if r.TotalCents != 1999 || r.ShippingCents != 300 {
		t.Fatalf("bad quote %+v", r)
	}
	items := d.Client.OrderItem.Query().Order(ent.Asc(orderitem.FieldID)).AllX(ctx)
	if items[0].PaidAmount+items[1].PaidAmount != 699 || items[2].PaidAmount != 1000 || items[0].ShippingAmount+items[1].ShippingAmount != 300 {
		t.Fatalf("incorrect allocation %+v", items)
	}
}
func TestPhysicalRejectStaleQuoteAndAtomicOversell(t *testing.T) {
	ctx := context.Background()
	d, uc := seedPhysical(t)
	in := quotePhysical(t, uc, physicalInput())
	d.Client.Product.UpdateOneID(1).SetShippingFee(400).ExecX(ctx)
	if _, e := uc.CreateOrder(ctx, in); e == nil {
		t.Fatal("stale quote accepted")
	}
	if d.Client.Product.GetX(ctx, 1).PhysicalStock != 5 {
		t.Fatal("stale quote reserved inventory")
	}
	in = quotePhysical(t, uc, physicalInput())
	d.Client.Product.UpdateOneID(1).SetPhysicalStock(1).ExecX(ctx)
	if _, e := uc.CreateOrder(ctx, in); e == nil {
		t.Fatal("oversell accepted")
	}
	if d.Client.Order.Query().CountX(ctx) != 0 || d.Client.Product.GetX(ctx, 1).PhysicalStock != 1 {
		t.Fatal("failed transaction leaked")
	}
	bad := physicalInput()
	bad.ShippingAddress["region"] = "Ontario"
	bad.QuoteOnly = true
	if _, e := uc.CreateOrder(ctx, bad); e == nil {
		t.Fatal("foreign region accepted")
	}
}
func TestPhysicalExpiryAndZeroPayable(t *testing.T) {
	ctx := context.Background()
	d, uc := seedPhysical(t)
	in := quotePhysical(t, uc, physicalInput())
	r, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	o := d.Client.Order.Query().Where(entorder.OrderNo(r.OrderNo)).OnlyX(ctx)
	d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().UTC().Add(-time.Hour)).ExecX(ctx)
	if e = uc.cancelOrder(ctx, r.OrderNo, "expiry", "system", 0, true); e != nil {
		t.Fatal(e)
	}
	if d.Client.Product.GetX(ctx, 1).PhysicalStock != 5 {
		t.Fatal("expiry leaked stock")
	}
	d.Client.Product.UpdateOneID(1).SetShippingMode("free").SetPrice(0).ExecX(ctx)
	in = quotePhysical(t, uc, physicalInput())
	r, e = uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	o = d.Client.Order.Query().Where(entorder.OrderNo(r.OrderNo)).OnlyX(ctx)
	if o.Status != "paid" || o.TotalAmount != 0 || o.ShippingStatus != "pending" || d.Client.Payment.Query().CountX(ctx) != 0 {
		t.Fatalf("zero payment behavior %+v", o)
	}
}

// A single SQLite connection detects accidental reads outside the active transaction.
type physicalDBSettings struct{ d *data.Data }

func (s physicalDBSettings) GetJSON(ctx context.Context, group, key string) ([]byte, error) {
	_, e := data.Client(ctx, s.d).Setting.Query().Count(ctx)
	if key == "query_password_required" {
		return []byte("true"), e
	}
	if key == "contact_required" {
		return []byte(`"none"`), e
	}
	return nil, e
}
func TestPhysicalQuoteUsesTransactionForSettings(t *testing.T) {
	d, uc := seedPhysical(t)
	d.DB.SetMaxOpenConns(1)
	uc.Settings = physicalDBSettings{d}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	in := physicalInput()
	in.Contact = "test@example.invalid"
	in.QuoteOnly = true
	if _, e := uc.CreateOrder(ctx, in); e != nil {
		t.Fatal(e)
	}
	if e := ctx.Err(); e != nil {
		t.Fatal("checkout read settings outside transaction", e)
	}
}
