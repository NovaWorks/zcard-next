package catalog

// P2-10 S1 轻量维护端口测试：UpdateUpstreamPrice / UpdateUpstreamStatus /
// ShelveOffMissing（删除对账——上游未见商品批量下架）。

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	_ "modernc.org/sqlite"
)

func newMaintainEnv(t *testing.T) (*data.Data, *ProductRepoImpl) {
	t.Helper()
	d, _ := newStatsEnv(t)
	return d, NewProductRepoImpl(d, nil)
}

// seedUpstream 建三个上游绑定商品（conn=9）：A/B 上架，C 已下架。
func seedUpstream(t *testing.T, d *data.Data) (a, b, c *ent.Product) {
	t.Helper()
	ctx := context.Background()
	mk := func(code string, status int8) *ent.Product {
		p, err := d.Client.Product.Create().SetSubsiteID(0).
			SetName("P-" + code).SetSlug("s-" + code).
			SetPrice(1000).SetStockType("card").SetStatus(status).
			SetUpstreamSourceID(9).SetUpstreamProductCode(code).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	return mk("A", 1), mk("B", 1), mk("C", 0)
}

func TestUpdateUpstreamPriceAndStatus(t *testing.T) {
	d, repo := newMaintainEnv(t)
	ctx := context.Background()
	a, _, _ := seedUpstream(t, d)

	found, err := repo.UpdateUpstreamPrice(ctx, 9, "A", 2345)
	if err != nil || !found {
		t.Fatalf("UpdateUpstreamPrice: found=%v err=%v", found, err)
	}
	got, _ := d.Client.Product.Get(ctx, a.ID)
	if got.Price != 2345 || got.Name != "P-A" {
		t.Fatalf("仅应改价不动名称: price=%d name=%s", got.Price, got.Name)
	}

	found, err = repo.UpdateUpstreamStatus(ctx, 9, "A", 2)
	if err != nil || !found {
		t.Fatalf("UpdateUpstreamStatus: found=%v err=%v", found, err)
	}
	got, _ = d.Client.Product.Get(ctx, a.ID)
	if got.Status != 2 {
		t.Fatalf("状态应更新为 2: %d", got.Status)
	}

	// 未导入商品：found=false 不报错
	found, err = repo.UpdateUpstreamPrice(ctx, 9, "NOPE", 1)
	if err != nil || found {
		t.Fatalf("未导入应 found=false: found=%v err=%v", found, err)
	}
}

func TestShelveOffMissing(t *testing.T) {
	d, repo := newMaintainEnv(t)
	ctx := context.Background()
	a, b, c := seedUpstream(t, d)

	// 上游仍见 A：B、C 应下架（C 已是 0 不重复计）
	n, err := repo.ShelveOffMissing(ctx, 9, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只下架 B 一件: %d", n)
	}
	for id, want := range map[uint64]int8{a.ID: 1, b.ID: 0, c.ID: 0} {
		got, _ := d.Client.Product.Get(ctx, id)
		if got.Status != want {
			t.Fatalf("商品 %d 状态错误: got %d want %d", id, got.Status, want)
		}
	}

	// 其他连接的商品不受影响
	other, _ := d.Client.Product.Create().SetSubsiteID(0).SetName("X").SetSlug("x").
		SetPrice(1).SetStockType("card").SetStatus(1).
		SetUpstreamSourceID(8).SetUpstreamProductCode("A").Save(ctx)
	if _, err := repo.ShelveOffMissing(ctx, 9, []string{"A"}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Product.Get(ctx, other.ID)
	if got.Status != 1 {
		t.Fatal("其他连接的商品不得被下架")
	}
}
