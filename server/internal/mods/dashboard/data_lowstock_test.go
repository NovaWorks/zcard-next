package dashboard

// 工作台统计测试：库存预警（GroupBy Scan 列映射——product_id 必须经 db tag 对齐）。

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// TestLowStockCount 库存预警：上架商品中可用卡密 < 阈值计数；下架/分站商品不参与。
func TestLowStockCount(t *testing.T) {
	d := newDashboardData(t)
	repo := NewDashboardRepoImpl(d)
	ctx := context.Background()

	p1 := d.Client.Product.Create().SetName("P1").SetSlug("p1").SetSubsiteID(0).SetStatus(1).SaveX(ctx)
	p2 := d.Client.Product.Create().SetName("P2").SetSlug("p2").SetSubsiteID(0).SetStatus(1).SaveX(ctx)
	off := d.Client.Product.Create().SetName("OFF").SetSlug("off").SetSubsiteID(0).SetStatus(0).SaveX(ctx) // 下架不参与
	for i := 0; i < 3; i++ { // 3 张 < 阈值 5 → 预警
		d.Client.Card.Create().SetProductID(p1.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("p1-%d", i)).SaveX(ctx)
	}
	for i := 0; i < 8; i++ { // 8 张 ≥ 阈值 → 不预警
		d.Client.Card.Create().SetProductID(p2.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("p2-%d", i)).SaveX(ctx)
	}
	d.Client.Card.Create().SetProductID(off.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash("off-1").SaveX(ctx)

	low, err := repo.GetLowStockCount(ctx, 5)
	if err != nil {
		t.Fatalf("GetLowStockCount: %v", err)
	}
	if low != 1 {
		t.Fatalf("低库存数 = %d, want 1", low)
	}
}

// TestLowStockCountSubsiteIsolation 分站隔离：只统计本站上架商品。
func TestLowStockCountSubsiteIsolation(t *testing.T) {
	d := newDashboardData(t)
	repo := NewDashboardRepoImpl(d)
	ctx := context.Background()

	pMain := d.Client.Product.Create().SetName("PM").SetSlug("pm").SetSubsiteID(0).SetStatus(1).SaveX(ctx)
	pSub := d.Client.Product.Create().SetName("PS").SetSlug("ps").SetSubsiteID(5).SetStatus(1).SaveX(ctx)
	for i := 0; i < 2; i++ {
		d.Client.Card.Create().SetProductID(pMain.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("m-%d", i)).SaveX(ctx)
		d.Client.Card.Create().SetProductID(pSub.ID).SetSubsiteID(5).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("s-%d", i)).SaveX(ctx)
	}

	// 主站视角：只见主站 1 个低库存商品
	mainCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 0, IsMain: true})
	low, err := repo.GetLowStockCount(mainCtx, 5)
	if err != nil {
		t.Fatalf("GetLowStockCount(main): %v", err)
	}
	if low != 1 {
		t.Fatalf("主站低库存数 = %d, want 1", low)
	}
	// 分站视角：只见分站 1 个
	subCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 5, IsMain: false})
	low2, err := repo.GetLowStockCount(subCtx, 5)
	if err != nil {
		t.Fatalf("GetLowStockCount(sub): %v", err)
	}
	if low2 != 1 {
		t.Fatalf("分站低库存数 = %d, want 1", low2)
	}
}
