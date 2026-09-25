package supply

import (
	"context"
	"log/slog"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

func TestImportProductsReimportsDeletedDujiaoProduct(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "独角测试")
	// 公网字面地址仅供适配器构造校验；目录全部来自下面的预览缓存，不发出请求。
	sealed, err := repo.SealCredentials("dujiao_next", "https://8.8.8.8", `{"api_key":"test","api_secret":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetDriver("dujiao_next").SetBaseURL("https://8.8.8.8").SetCredentials(sealed).SaveX(ctx)
	writer := catalog.NewProductRepoImpl(d, nil)
	syncSvc := &SyncService{repo: repo, writer: writer, log: slog.Default()}
	svc := &AdminSupplyService{repo: repo, sync: syncSvc}
	old := d.Client.Product.Create().SetName("旧商品").SetSlug("13").SetStatus(-1).SetPrice(500).SetDirectContent([]byte("history-cipher")).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("13").SaveX(ctx)
	oldSKU := d.Client.ProductSku.Create().SetProductID(old.ID).SetName("旧规格").SetSpecValues(map[string]string{}).SetPrice(500).SetUpstreamSkuID("27").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("history").SetStatus(order.StatusCompleted).SetTotalAmount(500).SaveX(ctx)
	item := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(old.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetFulfillmentType("auto").SetFulfillmentStatus("delivered").SaveX(ctx)
	input := catalogport.UpstreamProductInput{ConnectionID: conn.ID, UpstreamProductCode: "13", Name: "自动同步", Status: 1}
	if _, _, err := writer.UpsertUpstreamProduct(ctx, input); err == nil {
		t.Fatal("automatic sync bypassed archive")
	}
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"13", "13"}, PricingMode: "fixed", MarkupAmountCents: 100}
	// 使用已解析的独角目录预览，网络协议解析由 adapter 契约测试覆盖。
	importProducts := func() (*adminv1.ImportProductsReply, error) {
		return runSelectedImportForTest(t, svc, req, map[string]adapter.Product{
			"13": {ID: "13", Name: "Instagram", Description: "新详情", CategoryID: "6", Price: 1200, FactoryPrice: 1200, IsActive: true, Stock: 8,
				SKUs: []adapter.SKU{{ID: "27", Code: "27", Name: "标准", Price: 1200, Stock: 8, IsActive: true, SpecValues: map[string]string{"类型": "标准"}}}},
		})
	}
	reply, err := importProducts()
	if err != nil {
		t.Fatal(err)
	}
	if reply.Imported != 1 || reply.Updated != 0 || reply.Failed != 0 {
		t.Fatalf("import: %+v", reply)
	}
	m, err := repo.GetMapping(ctx, conn.ID, "13", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.LocalProductID == old.ID || m.UpStock != 8 {
		t.Fatalf("mapping: %+v", m)
	}
	fresh := d.Client.Product.GetX(ctx, m.LocalProductID)
	if fresh.Price != 1300 || fresh.Status != 1 || fresh.Description != "新详情" {
		t.Fatalf("new product: %+v", fresh)
	}
	sku := d.Client.ProductSku.Query().Where(productsku.ProductID(fresh.ID)).OnlyX(ctx)
	if sku.UpstreamSkuID != "27" || sku.Price != 1300 {
		t.Fatalf("SKU import lost identity/pricing: %+v", sku)
	}
	archive := d.Client.Product.GetX(ctx, old.ID)
	if archive.Status != -1 || archive.Name != old.Name || string(archive.DirectContent) != "history-cipher" || d.Client.OrderItem.GetX(ctx, item.ID).ProductID != old.ID || d.Client.ProductSku.GetX(ctx, oldSKU.ID).Price != 500 {
		t.Fatal("historical data changed")
	}
	reply, err = importProducts()
	if err != nil || reply.Imported != 0 || reply.Updated != 1 || reply.Failed != 0 {
		t.Fatalf("repeat: %+v %v", reply, err)
	}
	if d.Client.Product.Query().CountX(ctx) != 2 {
		t.Fatal("duplicate product created")
	}
	// 待定价的再次导入不能清零已有商品和规格价格。
	req.PricingMode = "pending"
	if _, err := importProducts(); err != nil {
		t.Fatal(err)
	}
	if p := d.Client.Product.GetX(ctx, fresh.ID); p.Status != 0 || p.Price != 1300 {
		t.Fatalf("pending: %+v", p)
	}
	if d.Client.ProductSku.GetX(ctx, sku.ID).Price != 1300 {
		t.Fatal("pending reset SKU price")
	}
}

func TestReimportRollsBackArchiveWhenMappingFails(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "rollback")
	writer := catalog.NewProductRepoImpl(d, nil)
	svc := &SyncService{repo: repo, writer: writer, log: slog.Default()}
	old := d.Client.Product.Create().SetName("archive").SetSlug("13").SetStatus(-1).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("13").SaveX(ctx)
	if _, err := d.DB.ExecContext(ctx, `CREATE TRIGGER reject_import_mapping BEFORE INSERT ON supply_mappings BEGIN SELECT RAISE(ABORT, 'mapping unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	p := &adapter.Product{ID: "13", Name: "new", Price: 1000, IsActive: true, SKUs: []adapter.SKU{{Code: "27", Name: "standard", Price: 1000, SpecValues: map[string]string{}}}}
	if _, err := svc.ImportOne(ctx, conn, p, nil, "equal", 0, 0); err == nil {
		t.Fatal("expected mapping failure")
	}
	if d.Client.Product.Query().CountX(ctx) != 1 || d.Client.Product.GetX(ctx, old.ID).UpstreamProductCode != "13" || d.Client.ProductSku.Query().CountX(ctx) != 0 {
		t.Fatal("partial import survived rollback")
	}
	if _, _, err := writer.UpsertUpstreamProduct(ctx, catalogport.UpstreamProductInput{ConnectionID: conn.ID, UpstreamProductCode: "13"}); err == nil {
		t.Fatal("failed import removed automatic sync protection")
	}
	if _, err := d.DB.ExecContext(ctx, `DROP TRIGGER reject_import_mapping`); err != nil {
		t.Fatal(err)
	}
	if created, err := svc.ImportOne(ctx, conn, p, nil, "equal", 0, 0); err != nil || !created {
		t.Fatalf("retry: %v %v", created, err)
	}
}

func TestReimportPendingProductStartsOffshelfWithoutNegativePrice(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "pending")
	writer := catalog.NewProductRepoImpl(d, nil)
	svc := &SyncService{repo: repo, writer: writer, log: slog.Default()}
	old := d.Client.Product.Create().SetName("archive").SetSlug("13").SetStatus(-1).SetPrice(900).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("13").SaveX(ctx)
	if created, err := svc.ImportOne(ctx, conn, &adapter.Product{ID: "13", Name: "pending", Price: 1200, IsActive: true}, nil, "pending", 0, 0); err != nil || !created {
		t.Fatalf("pending import: %v %v", created, err)
	}
	m, err := repo.GetMapping(ctx, conn.ID, "13", "")
	if err != nil {
		t.Fatal(err)
	}
	p := d.Client.Product.GetX(ctx, m.LocalProductID)
	if p.ID == old.ID || p.Price != 0 || p.Status != 0 {
		t.Fatalf("pending product: %+v", p)
	}
}
