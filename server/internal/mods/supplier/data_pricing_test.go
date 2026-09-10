package supplier

import (
	"context"
	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"math"
	"net/http/httptest"
	"testing"
)

func TestSupplierPricingPrecedenceAndLiveScope(t *testing.T) {
	r, d := newSupplierTestData(t)
	ctx := context.Background()
	a := seedAccount(t, r)
	root := d.Client.Category.Create().SetName("邮箱").SaveX(ctx)
	child := d.Client.Category.Create().SetName("企业邮箱").SetParentID(root.ID).SaveX(ctx)
	p := d.Client.Product.Create().SetName("邮箱账号").SetSlug("mail").SetPrice(1000).SetCategoryID(child.ID).SaveX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("年卡").SetSpecValues(map[string]string{"周期":"年"}).SaveX(ctx)
	save := func(scope string, cat, pid, sid uint64, price int64, discount int32) {
		t.Helper()
		if err := r.UpsertPriceRule(ctx, a, pid, sid, cat, scope, price, discount); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want int64) {
		t.Helper()
		rules, err := r.LoadPricing(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if got := rules.Price(p.ID, sku.ID, p.CategoryID, 1000); got != want {
			t.Fatalf("want %d got %d", want, got)
		}
	}
	save("global", 0, 0, 0, 0, 9000)
	check(900)
	save("category", root.ID, 0, 0, 0, 8000)
	check(800)
	save("category", child.ID, 0, 0, 0, 9500)
	check(950) // nearest scope wins, not lowest-price stacking
	save("product", 0, p.ID, 0, 700, 0)
	check(700)
	save("product", 0, p.ID, sku.ID, 650, 0)
	check(650)
	save("product", 0, p.ID, sku.ID, 600, 0)
	check(600)
	rows, _ := r.ListPrices(ctx, a)
	if len(rows) != 5 {
		t.Fatal("same scope duplicated", len(rows))
	}
	for _, row := range rows {
		if row.SkuID == sku.ID {
			if err := r.DeletePrice(ctx, row.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	check(700)
	rules, _ := r.LoadPricing(ctx, a)
	if got := rules.Price(9999, 0, child.ID, 2000); got != 1900 {
		t.Fatal("new product did not inherit category", got)
	}
	if got := rules.Price(9999, 0, 0, 1000); got != 900 {
		t.Fatal("uncategorized not global", got)
	}
	other, _ := r.LoadPricing(ctx, a+1)
	if other.Price(p.ID, sku.ID, child.ID, 1000) != 1000 {
		t.Fatal("cross-account pricing")
	}
	if got := rules.Price(9999, 0, 0, math.MaxInt64); got <= 0 {
		t.Fatal("overflow", got)
	}
	for _, invalid := range []struct {
		scope         string
		cat, pid, sid uint64
		price         int64
		discount      int32
	}{
		{"global", 0, 0, 0, 0, 0}, {"global", 0, 0, 0, 0, 10001}, {"category", 9999, 0, 0, 0, 9000}, {"product", 0, 9999, 0, 100, 0}, {"product", 0, p.ID, 9999, 100, 0}, {"bad", 0, 0, 0, 0, 9000},
	} {
		if err := r.UpsertPriceRule(ctx, a, invalid.pid, invalid.sid, invalid.cat, invalid.scope, invalid.price, invalid.discount); err == nil {
			t.Fatal("invalid rule saved", invalid)
		}
	}
}

func TestDiscountQuoteMatchesDebitAndOriginalOrderSnapshot(t *testing.T) {
	svc, r, catalog := newCompatEnv(t)
	account := seedCompatAccount(t, r, "zcard", "discount-account", "secret", 10000)
	ctx := context.WithValue(context.Background(), accountCtxKey{}, account.ID)
	if err := r.UpsertPriceRule(ctx, account.ID, 0, 0, 0, "global", 0, 9000); err != nil {
		t.Fatal(err)
	}
	reply, err := svc.ListProducts(ctx, &supplyv1.ListProductsRequest{})
	if err != nil || reply.Items[0].Price != 900 {
		t.Fatal(reply, err)
	}
	detail, err := svc.GetProduct(ctx, &supplyv1.GetProductRequest{Id: "1"})
	if err != nil || detail.Product.Price != 900 {
		t.Fatal(detail, err)
	}
	rules, _ := r.LoadPricing(ctx, account.ID)
	dj := (&dujiaoCompat{svc: svc}).dujiaoProduct(httptest.NewRequest("GET", "/", nil), account, catalog.prods[0], rules)
	acg := (&acgCompat{svc: svc}).acgProduct(ctx, account, catalog.prods[0], rules)
	if dj["price_amount"] != "9.00" || acg["price"] != "9.00" {
		t.Fatal("compat quote mismatch", dj, acg)
	}
	result, err := svc.fulfillOrder(ctx, account.ID, 1, 2, "discount-order", "", "")
	if err != nil || result.amount != 1800 || !result.delivered {
		t.Fatal(result, err)
	}
	got := r.data.Client.SupplierAccount.GetX(ctx, account.ID)
	if got.BalanceCache != 8200 {
		t.Fatal("wrong actual debit", got.BalanceCache)
	}
	if err := r.UpsertPriceRule(ctx, account.ID, 0, 0, 0, "global", 0, 8000); err != nil {
		t.Fatal(err)
	}
	again, err := svc.fulfillOrder(ctx, account.ID, 1, 2, "discount-order", "", "")
	if err != nil || again.amount != 1800 {
		t.Fatal("repriced old order", again, err)
	}
	if r.data.Client.SupplierAccount.GetX(ctx, account.ID).BalanceCache != 8200 {
		t.Fatal("duplicate debit")
	}
	if _, err := r.data.DB.Exec("DROP TABLE supplier_product_prices"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListProducts(ctx, &supplyv1.ListProductsRequest{}); err == nil {
		t.Fatal("pricing read failure ignored")
	}
	if _, err := svc.fulfillOrder(ctx, account.ID, 1, 1, "must-not-charge", "", ""); err == nil {
		t.Fatal("pricing failure charged base price")
	}
	if r.data.Client.SupplierAccount.GetX(ctx, account.ID).BalanceCache != 8200 {
		t.Fatal("charged on read error")
	}
}
