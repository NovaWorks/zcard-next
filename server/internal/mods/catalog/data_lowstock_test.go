package catalog

// 低库存筛选测试：ListAdmin 的 LowStockThreshold 过滤——
// 仅卡密类、可用卡密 < 阈值；无卡密商品视为 0；链接类/分站/非上架不混入。

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// TestListAdminLowStock 低库存过滤：阈值 5，P1=3 张预警、P2=8 张不预警、P3 无卡密预警、P4 链接类忽略。
func TestListAdminLowStock(t *testing.T) {
	d, _ := newStatsEnv(t)
	repo := NewProductRepoImpl(d, nil)
	ctx := context.Background()

	p1 := d.Client.Product.Create().SetSubsiteID(0).SetName("P1").SetSlug("p1").SetPrice(1000).SetStockType("card").SetStatus(1).SaveX(ctx)
	p2 := d.Client.Product.Create().SetSubsiteID(0).SetName("P2").SetSlug("p2").SetPrice(1000).SetStockType("card").SetStatus(1).SaveX(ctx)
	p3 := d.Client.Product.Create().SetSubsiteID(0).SetName("P3").SetSlug("p3").SetPrice(1000).SetStockType("card").SetStatus(1).SaveX(ctx) // 无卡密
	d.Client.Product.Create().SetSubsiteID(0).SetName("P4").SetSlug("p4").SetPrice(1000).SetStockType("url").SetStatus(1).SaveX(ctx)  // 链接类忽略
	d.Client.Product.Create().SetSubsiteID(0).SetName("P5").SetSlug("p5").SetPrice(1000).SetStockType("card").SetStatus(0).SaveX(ctx) // 下架不参与
	for i := 0; i < 3; i++ {
		d.Client.Card.Create().SetProductID(p1.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("p1-%d", i)).SaveX(ctx)
	}
	for i := 0; i < 8; i++ {
		d.Client.Card.Create().SetProductID(p2.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("p2-%d", i)).SaveX(ctx)
	}

	rows, total, err := repo.ListAdmin(ctx, port.AdminFilter{Page: 1, PageSize: 20, LowStockThreshold: 5})
	if err != nil {
		t.Fatalf("ListAdmin: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("低库存 total=%d rows=%d, want 2（P1+P3）", total, len(rows))
	}
	got := map[uint64]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	if !got[p1.ID] || !got[p3.ID] {
		t.Fatalf("低库存集合错误: %+v, want P1+P3", got)
	}
	// 未开启筛选：全部 5 个商品（管理面含下架/隐藏）
	_, totalAll, err := repo.ListAdmin(ctx, port.AdminFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListAdmin(all): %v", err)
	}
	if totalAll != 5 {
		t.Fatalf("全量 total=%d, want 5", totalAll)
	}
}

// TestListAdminLowStockSubsiteIsolation 分站隔离：低库存只统计本站卡密。
func TestListAdminLowStockSubsiteIsolation(t *testing.T) {
	d, _ := newStatsEnv(t)
	repo := NewProductRepoImpl(d, nil)
	ctx := context.Background()

	pMain := d.Client.Product.Create().SetSubsiteID(0).SetName("PM").SetSlug("pm").SetPrice(1000).SetStockType("card").SetStatus(1).SaveX(ctx)
	pSub := d.Client.Product.Create().SetSubsiteID(5).SetName("PS").SetSlug("ps").SetPrice(1000).SetStockType("card").SetStatus(1).SaveX(ctx)
	for i := 0; i < 2; i++ {
		d.Client.Card.Create().SetProductID(pMain.ID).SetSubsiteID(0).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("m-%d", i)).SaveX(ctx)
		d.Client.Card.Create().SetProductID(pSub.ID).SetSubsiteID(5).SetContent([]byte("x")).SetContentHash(fmt.Sprintf("s-%d", i)).SaveX(ctx)
	}

	mainCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 0, IsMain: true})
	_, totalMain, err := repo.ListAdmin(mainCtx, port.AdminFilter{Page: 1, PageSize: 20, LowStockThreshold: 5})
	if err != nil {
		t.Fatalf("ListAdmin(main): %v", err)
	}
	if totalMain != 1 {
		t.Fatalf("主站低库存 total=%d, want 1", totalMain)
	}
	subCtx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 5, IsMain: false})
	_, totalSub, err := repo.ListAdmin(subCtx, port.AdminFilter{Page: 1, PageSize: 20, LowStockThreshold: 5})
	if err != nil {
		t.Fatalf("ListAdmin(sub): %v", err)
	}
	if totalSub != 1 {
		t.Fatalf("分站低库存 total=%d, want 1", totalSub)
	}
}
