//go:build integration

package testint

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"sync"
	"testing"
)

func TestSupplierPricingMySQL(t *testing.T) { runSupplierPricing(MySQL(t)) }
func TestSupplierPricingPG(t *testing.T)    { runSupplierPricing(PG(t)) }
func runSupplierPricing(h *Harness) {
	t := h.T
	ctx := context.Background()
	box, _ := crypto.NewBox(make([]byte, 32))
	r := supplier.NewSupplierRepoImpl(h.Data, box)
	a, err := r.CreateAccount(ctx, "pricing", "pricing-test", "secret", "", "zcard", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.Data.Client.Category.Create().SetName("分类").SaveX(ctx)
	child := h.Data.Client.Category.Create().SetName("子分类").SetParentID(c.ID).SaveX(ctx)
	if err = r.UpsertPriceRule(ctx, a.ID, 0, 0, 0, "global", 0, 9000); err != nil {
		t.Fatal(err)
	}
	if err = r.UpsertPriceRule(ctx, a.ID, 0, 0, c.ID, "category", 0, 8000); err != nil {
		t.Fatal(err)
	}
	if err = r.UpsertPriceRule(ctx, a.ID, 0, 0, child.ID, "category", 0, 7500); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- r.UpsertPriceRule(ctx, a.ID, 0, 0, child.ID, "category", 0, 7000) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rules, err := r.LoadPricing(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rules.Price(999, 0, child.ID, 1000) != 700 {
		t.Fatal("wrong inherited price")
	}
	rows, err := r.ListPrices(ctx, a.ID)
	if err != nil || len(rows) != 3 {
		t.Fatal("unique scope violated", len(rows), err)
	}
}
