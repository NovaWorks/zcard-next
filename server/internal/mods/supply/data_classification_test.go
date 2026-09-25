package supply

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"testing"
)

func TestCategoryRuleMatchingAndProtectedProducts(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	c := s.repo.entClient(ctx)
	a := c.Category.Create().SetName("账号").SaveX(ctx)
	b := c.Category.Create().SetName("手动").SaveX(ctx)
	p := c.Product.Create().SetName("account").SetSlug("protected").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("protected").SetCategoryID(b.ID).SetCategoryProtected(true).SaveX(ctx)
	products := map[string]adapter.Product{"new": {Name: "ＡＰＰＬＥ 小火箭", CategoryID: "g"}, "protected": {Name: "Apple 共享账号", CategoryID: "g"}, "excluded": {Name: "Apple 独享账号", CategoryID: "g"}}
	rules := []*adminv1.ProductCategoryRule{{Keywords: []string{"apple"}, Excludes: []string{"独享"}, CategoryId: a.ID}}
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"new", "protected", "excluded"}, CategoryRules: rules}
	got, e := s.resolveImportCategories(ctx, req, products, map[string]uint64{"g": b.ID})
	if e != nil {
		t.Fatal(e)
	}
	if got["new"] != a.ID {
		t.Fatal("unicode keyword did not match")
	}
	if _, ok := got["protected"]; ok {
		t.Fatal("rule overwrote manual category")
	}
	if _, ok := got["excluded"]; ok {
		t.Fatal("exclusion ignored")
	}
	req.ProductCategories = map[string]uint64{"protected": a.ID}
	got, e = s.resolveImportCategories(ctx, req, products, nil)
	if e != nil || got["protected"] != a.ID {
		t.Fatalf("explicit override: %v %v", got, e)
	}
	if c.Product.GetX(ctx, p.ID).CategoryID != b.ID {
		t.Fatal("preview mutated product")
	}
	req.ProductCategories = map[string]uint64{"unselected": a.ID}
	if _, e = s.resolveImportCategories(ctx, req, products, nil); e == nil {
		t.Fatal("unselected override accepted")
	}
	req.ProductCategories = nil
	req.CategoryRules[0].CategoryId = 99999
	if _, e = s.resolveImportCategories(ctx, req, products, nil); e == nil {
		t.Fatal("missing category accepted")
	}
}
func TestSelectedCategoryDoesNotPersistWholeGroupMapping(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	c := s.repo.entClient(ctx)
	a := c.Category.Create().SetName("group").SaveX(ctx)
	b := c.Category.Create().SetName("item").SaveX(ctx)
	c.SupplyConnection.UpdateOneID(conn.ID).SetSettings(map[string]any{"category_map": map[string]any{"g": a.ID}}).ExecX(ctx)
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"p"}, CategoryMap: map[string]uint64{"g": b.ID}, SelectedCategoriesOnly: true, SaveCategoryRules: true, CategoryRules: []*adminv1.ProductCategoryRule{{Keywords: []string{"apple"}, CategoryId: b.ID}}}
	cats, e := s.saveImportCategories(ctx, req, map[string]adapter.Product{"p": {Name: "Apple", CategoryID: "g"}}, PriceModeEqual, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	if cats["g"] != b.ID {
		t.Fatal("selected preview mapping missing")
	}
	fresh := c.SupplyConnection.GetX(ctx, conn.ID)
	if categoryMapFromSettings(fresh.Settings)["g"] != a.ID {
		t.Fatal("whole group mapping mutated")
	}
	if fresh.Settings["category_rules"] == nil {
		t.Fatal("rules were not saved")
	}
}
