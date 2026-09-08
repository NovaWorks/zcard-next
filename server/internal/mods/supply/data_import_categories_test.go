package supply

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"testing"
)

func TestImportCategoryDraftCommitAndRetry(t *testing.T) {
	repo, d := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, repo, d, "导入")
	svc := NewAdminSupplyService(repo, nil)
	parent := d.Client.Category.Create().SetName("账号").SaveX(ctx)
	// 同名但不同位置不能误复用。
	other := d.Client.Category.Create().SetName("邮箱").SaveX(ctx)
	products := map[string]adapter.Product{"p": {ID: "p", CategoryID: "mail"}}
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"p"}, CategoryDrafts: []*adminv1.ImportCategoryDraft{{UpstreamCode: "mail", Name: " 邮箱 ", ParentId: parent.ID}}}
	if got := d.Client.Category.Query().CountX(ctx); got != 2 {
		t.Fatal(got)
	}
	mapping, err := svc.saveImportCategories(ctx, req, products, "percent", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := mapping["mail"]
	if id == 0 || id == other.ID {
		t.Fatal(mapping)
	}
	cat := d.Client.Category.GetX(ctx, id)
	if cat.ParentID != parent.ID || cat.Name != "邮箱" {
		t.Fatal(cat)
	}
	again, err := svc.saveImportCategories(ctx, req, products, "percent", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if again["mail"] != id || d.Client.Category.Query().CountX(ctx) != 3 {
		t.Fatal("重试创建了重复分类")
	}
	saved := d.Client.SupplyConnection.GetX(ctx, conn.ID)
	if categoryMapFromSettings(saved.Settings)["mail"] != id {
		t.Fatal("映射未保存")
	}
}

func TestImportCategoriesRejectUnselectedAndRollback(t *testing.T) {
	repo, d := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, repo, d, "校验")
	svc := NewAdminSupplyService(repo, nil)
	products := map[string]adapter.Product{"p": {CategoryID: "a"}, "q": {CategoryID: "b"}}
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"p"}, CategoryDrafts: []*adminv1.ImportCategoryDraft{{UpstreamCode: "b", Name: "不能创建"}}}
	if _, err := svc.saveImportCategories(ctx, req, products, "equal", 0, 0); err == nil {
		t.Fatal("未拒绝未选分类")
	}
	req.Codes = []string{"p", "q"}
	req.CategoryDrafts = []*adminv1.ImportCategoryDraft{{UpstreamCode: "a", Name: "必须回滚"}, {UpstreamCode: "b", Name: "无效父级", ParentId: 99999}}
	if _, err := svc.saveImportCategories(ctx, req, products, "equal", 0, 0); err == nil {
		t.Fatal("未拒绝不存在的父级")
	}
	if d.Client.Category.Query().CountX(ctx) != 0 {
		t.Fatal("事务失败留下分类")
	}
	if len(categoryMapFromSettings(d.Client.SupplyConnection.GetX(ctx, conn.ID).Settings)) != 0 {
		t.Fatal("事务失败留下映射")
	}
	foreign := d.Client.Category.Create().SetSubsiteID(88).SetName("其他站").SaveX(ctx)
	req.CategoryDrafts = nil
	req.CategoryMap = map[string]uint64{"a": foreign.ID}
	if _, err := svc.saveImportCategories(ctx, req, products, "equal", 0, 0); err == nil {
		t.Fatal("跨站分类未拒绝")
	}
	req.CategoryMap = nil
	req.Codes = []string{"gone"}
	if _, err := svc.saveImportCategories(ctx, req, products, "equal", 0, 0); err == nil {
		t.Fatal("失效商品未拒绝")
	}
}

func TestImportExplicitClearSurvivesSyncFallback(t *testing.T) {
	syncSvc, repo, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "清除")
	cat := repo.entClient(ctx).Category.Create().SetName("旧分类").SaveX(ctx)
	repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetSettings(map[string]any{"category_map": map[string]any{"a": cat.ID, "untouched": cat.ID}}).SaveX(ctx)
	if err := repo.UpsertMapping(ctx, &ent.SupplyMapping{ConnectionID: conn.ID, UpstreamProduct: "p", UpstreamCategory: "a", LocalCategoryID: cat.ID, LocalProductID: 50}); err != nil {
		t.Fatal(err)
	}
	svc := NewAdminSupplyService(repo, syncSvc)
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"p"}, CategoryMap: map[string]uint64{"a": 0}}
	mapping, err := svc.saveImportCategories(ctx, req, map[string]adapter.Product{"p": {ID: "p", CategoryID: "a"}}, "equal", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := mapping["a"]; !ok || v != 0 {
		t.Fatal(mapping)
	}
	fresh := repo.entClient(ctx).SupplyConnection.GetX(ctx, conn.ID)
	saved := categoryMapFromSettings(fresh.Settings)
	if v, ok := saved["a"]; !ok || v != 0 || saved["untouched"] != cat.ID {
		t.Fatal(saved)
	}
	_, err = syncSvc.ImportOne(ctx, fresh, &adapter.Product{ID: "p", CategoryID: "a", Name: "p"}, saved, "equal", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	writer := syncSvc.writer.(*fakeWriter)
	if !writer.calls[0].CategorySet || writer.calls[0].CategoryID != 0 {
		t.Fatal("未向商品写入显式清除")
	}
}

// 模拟同步开始时缓存旧分类，此后运营已经合并/重映射。
func TestImportUsesLatestCategoryDuringRunningSync(t *testing.T) {
	svc, repo, writer, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "正在同步")
	cat := repo.entClient(ctx).Category.Create().SetName("新目标").SaveX(ctx)
	repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetSettings(map[string]any{"category_map": map[string]any{"a": cat.ID}}).SaveX(ctx)
	stale := map[string]uint64{"a": 999}
	if _, err := svc.ImportOne(ctx, conn, &adapter.Product{ID: "p", CategoryID: "a", Name: "p"}, stale, "equal", 0, 0); err != nil {
		t.Fatal(err)
	}
	if writer.calls[0].CategoryID != cat.ID {
		t.Fatalf("缓存映射覆盖了最新设置 got=%d want=%d settings=%#v", writer.calls[0].CategoryID, cat.ID, repo.entClient(ctx).SupplyConnection.GetX(ctx, conn.ID).Settings)
	}
	// 旧渠道只有 mapping 行，没有 settings.category_map，也须读取迁移后的映射。
	repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetSettings(map[string]any{}).SaveX(ctx)
	if _, err := svc.ImportOne(ctx, conn, &adapter.Product{ID: "p", CategoryID: "a", Name: "p"}, stale, "equal", 0, 0); err != nil {
		t.Fatal(err)
	}
	if writer.calls[1].CategoryID != cat.ID {
		t.Fatal("未读取最新映射行")
	}
}

func TestSaveMappingAfterMergeDoesNotRestoreDeletedCategory(t *testing.T) {
	svc, repo, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "合并窗口")
	cat := repo.entClient(ctx).Category.Create().SetName("目标").SaveX(ctx)
	p := repo.entClient(ctx).Product.Create().SetName("已迁移商品").SetSlug("merged-product").SetPrice(100).SetCategoryID(cat.ID).SaveX(ctx)
	stale := &ent.SupplyMapping{ConnectionID: conn.ID, UpstreamProduct: "p", UpstreamCategory: "a", LocalProductID: p.ID, LocalCategoryID: 99999}
	if err := svc.saveProductMapping(ctx, stale); err != nil {
		t.Fatal(err)
	}
	m, err := repo.GetMapping(ctx, conn.ID, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.LocalCategoryID != cat.ID {
		t.Fatal("映射写入恢复了已删除分类")
	}
}
