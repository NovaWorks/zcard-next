package catalog

import (
	"context"
	"fmt"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestCategoryCountsAndLotteryProductFilter(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	c := d.Client
	parent := c.Category.Create().SetName("父分类").SaveX(ctx)
	child := c.Category.Create().SetName("子分类").SetParentID(parent.ID).SaveX(ctx)
	for i := 0; i < 60; i++ {
		c.Product.Create().SetName("上游商品").SetSlug(fmt.Sprint("up-", i)).SetCategoryID(child.ID).SetUpstreamSourceID(9).SetStatus(1).SaveX(ctx)
	}
	own := c.Product.Create().SetName("自营卡密").SetSlug("own").SetCategoryID(parent.ID).SetUpstreamSourceID(0).SetStatus(1).SaveX(ctx)
	c.Product.Create().SetName("自营链接").SetSlug("link").SetStockType("url").SetCategoryID(child.ID).SetStatus(1).SaveX(ctx)
	c.Product.Create().SetName("隐藏商品").SetSlug("hidden").SetCategoryID(child.ID).SetStatus(2).SaveX(ctx)
	c.Product.Create().SetName("已删除").SetSlug("deleted").SetCategoryID(child.ID).SetStatus(-1).SaveX(ctx)
	c.Product.Create().SetSubsiteID(9).SetName("其他站点").SetSlug("other").SetCategoryID(child.ID).SetStatus(1).SaveX(ctx)
	list, e := s.ListProducts(ctx, &adminv1.ListProductsRequest{LocalOnly: true, StockType: "card", Status: 1, PageSize: 50})
	if e != nil || list.Total != 1 || list.Products[0].Id != own.ID {
		t.Fatalf("filter: %+v %v", list, e)
	}
	cats, e := s.ListCategories(ctx, &emptypb.Empty{})
	if e != nil {
		t.Fatal(e)
	}
	counts := map[uint64]int64{}
	for _, v := range cats.Categories {
		counts[v.Id] = v.ProductCount
	}
	if counts[parent.ID] != 63 || counts[child.ID] != 62 {
		t.Fatal("counts", counts)
	}
	c.Product.UpdateOneID(own.ID).SetCategoryID(child.ID).ExecX(ctx)
	cats, e = s.ListCategories(ctx, &emptypb.Empty{})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range cats.Categories {
		if v.ProductCount != 63 {
			t.Fatal("move not reflected", v)
		}
	}
}
