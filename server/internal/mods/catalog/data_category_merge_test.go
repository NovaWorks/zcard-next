package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"testing"
)

func TestMergeCategoriesPreviewCommitAndMapping(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	source := d.Client.Category.Create().SetName("来源").SaveX(ctx)
	target := d.Client.Category.Create().SetName("目标").SaveX(ctx)
	child := d.Client.Category.Create().SetName("保留子类").SetParentID(source.ID).SaveX(ctx)
	p := d.Client.Product.Create().SetName("p").SetSlug("merge-p").SetPrice(100).SetCategoryID(source.ID).SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetName("上游").SetDriver("zcard").SetBaseURL("https://example.com").SetCredentials([]byte("x")).SetSettings(map[string]any{"category_map": map[string]any{"a": source.ID}, "keep": true}).SaveX(ctx)
	m := d.Client.SupplyMapping.Create().SetConnectionID(conn.ID).SetUpstreamProduct("p").SetLocalProductID(p.ID).SetLocalCategoryID(source.ID).SaveX(ctx)
	req := &adminv1.MergeCategoriesRequest{SourceIds: []uint64{source.ID}, TargetId: target.ID, Preview: true}
	reply, err := svc.MergeCategories(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Products != 1 || reply.Children != 1 || reply.Categories != 1 {
		t.Fatal(reply)
	}
	if d.Client.Product.GetX(ctx, p.ID).CategoryID != source.ID || d.Client.Category.Query().CountX(ctx) != 3 {
		t.Fatal("预览修改了数据")
	}
	req.Preview = false
	if _, err = svc.MergeCategories(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, p.ID).CategoryID != target.ID || d.Client.Category.GetX(ctx, child.ID).ParentID != target.ID {
		t.Fatal("未迁移商品或子类")
	}
	if d.Client.SupplyMapping.GetX(ctx, m.ID).LocalCategoryID != target.ID {
		t.Fatal("未迁移映射行")
	}
	settings := d.Client.SupplyConnection.GetX(ctx, conn.ID).Settings
	if settings["keep"] != true || uint64(settings["category_map"].(map[string]any)["a"].(float64)) != target.ID {
		t.Fatal(settings)
	}
	if d.Client.Category.Query().CountX(ctx) != 2 {
		t.Fatal("未删除来源")
	}
}

func TestMergeCategoriesRejectDescendantAndForeign(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	parent := d.Client.Category.Create().SetName("父").SaveX(ctx)
	child := d.Client.Category.Create().SetName("子").SetParentID(parent.ID).SaveX(ctx)
	foreign := d.Client.Category.Create().SetName("外站").SetSubsiteID(77).SaveX(ctx)
	for _, target := range []uint64{parent.ID, child.ID, foreign.ID, 999999} {
		if _, err := svc.MergeCategories(ctx, &adminv1.MergeCategoriesRequest{SourceIds: []uint64{parent.ID}, TargetId: target}); err == nil {
			t.Fatalf("未拒绝目标 %d", target)
		}
	}
	if d.Client.Category.Query().CountX(ctx) != 3 {
		t.Fatal("拒绝后有写入")
	}
}

func TestUpstreamExplicitCategoryClear(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	cat := d.Client.Category.Create().SetName("原分类").SaveX(ctx)
	input := port.UpstreamProductInput{ConnectionID: 12, UpstreamProductCode: "x", Name: "商品", Price: 100, CategoryID: cat.ID}
	id, _, err := svc.repo.UpsertUpstreamProduct(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.CategoryID = 0
	if _, _, err = svc.repo.UpsertUpstreamProduct(ctx, input); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, id).CategoryID != cat.ID {
		t.Fatal("缺省覆盖了原分类")
	}
	input.CategorySet = true
	if _, _, err = svc.repo.UpsertUpstreamProduct(ctx, input); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, id).CategoryID != 0 {
		t.Fatal("显式清除无效")
	}
}

func TestMergeCategoriesProtectsCouponScope(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	source := d.Client.Category.Create().SetName("优惠类").SaveX(ctx)
	target := d.Client.Category.Create().SetName("目标").SaveX(ctx)
	d.Client.Coupon.Create().SetName("范围券").SetCode("merge-coupon").SetType("fixed").SetValue(100).SetScope(map[string]any{"category_ids": []uint64{source.ID}}).SaveX(ctx)
	for _, preview := range []bool{true, false} {
		if _, err := svc.MergeCategories(ctx, &adminv1.MergeCategoriesRequest{SourceIds: []uint64{source.ID}, TargetId: target.ID, Preview: preview}); err == nil {
			t.Fatal("优惠范围引用未拦截")
		}
	}
	if d.Client.Category.Query().CountX(ctx) != 2 {
		t.Fatal("拦截后删除了分类")
	}
}
