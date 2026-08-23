package adapter

// acg_live_test.go — 真实上游联调测试（只读接口：connect/items/inventory/stock，
// 绝不 trade 下单）。默认跳过；设置环境变量后启用：
//
//	TGAO_BASE_URL=https://tghao.uk TGAO_APP_ID=2608 TGAO_APP_KEY=xxx \
//	  go test ./internal/mods/supply/adapter -run TestAcgLive -v -count=1

import (
	"context"
	"os"
	"testing"
	"time"
)

func liveAcgAdapter(t *testing.T) (*acgFakaAdapter, bool) {
	base := os.Getenv("TGAO_BASE_URL")
	if base == "" {
		t.Skip("未设置 TGAO_BASE_URL，跳过真实上游联调")
	}
	a, err := newAcgFaka(base, Credentials{
		AppID:  os.Getenv("TGAO_APP_ID"),
		AppKey: os.Getenv("TGAO_APP_KEY"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a.(*acgFakaAdapter), true
}

func TestAcgLivePing(t *testing.T) {
	a, _ := liveAcgAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := a.Ping(ctx)
	if err != nil {
		t.Fatalf("connect 验签失败: %v", err)
	}
	t.Logf("站点: %q", p.SiteName)
}

func TestAcgLiveCatalog(t *testing.T) {
	a, _ := liveAcgAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	list, err := a.ListProducts(ctx, 1, 50, true)
	if err != nil {
		t.Fatalf("items 拉取失败: %v", err)
	}
	active, inactive, withSKUs, skuTotal := 0, 0, 0, 0
	cats := map[string]int{}
	for _, p := range list.Items {
		cats[p.CategoryID]++
		if p.IsActive {
			active++
		} else {
			inactive++
		}
		if len(p.SKUs) > 0 {
			withSKUs++
			skuTotal += len(p.SKUs)
		}
	}
	widgets := 0
	for _, p := range list.Items {
		if p.IsActive {
			continue
		}
		if inv, err := a.fetchInventory(ctx, p.ID); err == nil && inv.Config == "__widget__" {
			widgets++
		}
	}
	t.Logf("商品总数=%d 在售=%d 下架=%d 规格品=%d SKU总数=%d 分类数=%d",
		list.Total, active, inactive, withSKUs, skuTotal, len(cats))
	_ = widgets
	for c, n := range cats {
		t.Logf("  分类[%s]: %d 品", c, n)
	}
	if list.Total == 0 {
		t.Fatal("商品为空——items 解析可能不兼容")
	}

	// 首个规格品：按组合查库存（stock 接口 race/sku 传参验证）
	for _, p := range list.Items {
		if len(p.SKUs) == 0 || !p.IsActive {
			continue
		}
		sk := p.SKUs[0]
		st, err := a.GetStock(ctx, p.ID, sk.Code)
		if err != nil {
			t.Fatalf("规格库存查询失败 %s/%s: %v", p.ID, sk.Code, err)
		}
		t.Logf("规格库存: %s [%s] = %d", p.Name, sk.Name, st)
		break
	}
	// 首个无规格在售品：普通库存查询
	for _, p := range list.Items {
		if len(p.SKUs) > 0 || !p.IsActive {
			continue
		}
		st, err := a.GetStock(ctx, p.ID, "")
		if err != nil {
			t.Fatalf("普通库存查询失败 %s: %v", p.ID, err)
		}
		t.Logf("普通库存: %s = %d", p.Name, st)
		break
	}
}
