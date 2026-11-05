package catalog

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/proto"
)

type copyTestPermissions map[string]bool

func (p copyTestPermissions) Allowed(_ context.Context, _ uint64, code string) bool {
	return p[code]
}

func (copyTestPermissions) PermissionsOf(context.Context, uint64) ([]string, error) {
	return nil, nil
}

func (copyTestPermissions) RoleName(context.Context, uint64) string { return "" }

func TestCreateProductCopiesConfigurationAtomically(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Realm: authn.RealmAdmin, RoleID: 1})
	permissions := copyTestPermissions{
		"catalog:read_detail": true, "catalog:sku_read": true, "catalog:sku_write": true,
		"catalog:control_read": true, "catalog:control_write": true,
	}
	svc.authz = permissions
	source := d.Client.Product.Create().SetName("SIM").SetSlug("sim").SetPrice(1000).
		SetGoodsType("physical").SetShippingCountries([]string{"CN"}).SetIsLocked(true).SaveX(ctx)
	original := d.Client.ProductSku.Create().SetProductID(source.ID).SetName("实体卡").
		SetSpecValues(map[string]string{"卡类": "实体卡"}).SetPrice(1200).SetCost(700).
		SetPhysicalStock(15).SetStockOffset(6).SetUpstreamSkuID("old-supplier-sku").SaveX(ctx)
	d.Client.ProductSku.Create().SetProductID(source.ID).SetName("eSIM卡").
		SetSpecValues(map[string]string{"卡类": "eSIM卡"}).SaveX(ctx)
	control := d.Client.ProductControl.Create().SetProductID(source.ID).SetName("填写账号").SetType("text").
		SetPlaceholder("账号").SetRequired(true).SetValidation("username").SetMaxLength(64).SetSort(3).SaveX(ctx)

	req := &adminv1.CreateProductRequest{
		Name: "SIM 副本", PriceCents: 1000, ProductProperty: proto.String("physical"),
		ShippingCountries: []string{"CN"}, CopyFromProductId: source.ID, PhysicalStock: proto.Int64(5),
	}
	if _, err := svc.CreateProduct(ctx, req); errors.Reason(err) != "catalog.SKU_BASE_STOCK" {
		t.Fatalf("expected inventory guidance, got %v", err)
	}
	if d.Client.Product.Query().CountX(ctx) != 1 || d.Client.ProductSku.Query().CountX(ctx) != 2 {
		t.Fatal("failed copy persisted a partial product")
	}
	req.PhysicalStock = proto.Int64(0)
	got, err := svc.CreateProduct(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := svc.repo.ListProductSkus(ctx, got.Id)
	if err != nil || len(rows) != 2 {
		t.Fatalf("copied SKUs %v, %v", rows, err)
	}
	first := rows[0]
	if first.ID == original.ID || first.Name != original.Name || first.SpecValues["卡类"] != "实体卡" ||
		first.Price != original.Price || first.Cost != original.Cost || first.PhysicalStock != 0 ||
		first.StockOffset != 0 || first.UpstreamSkuID != "" || rows[1].Name != "eSIM卡" || rows[1].Price != 0 {
		t.Fatalf("incorrect SKU copy: %+v", rows)
	}
	copied := d.Client.ProductControl.Query().Where(productcontrol.ProductID(got.Id)).OnlyX(ctx)
	if copied.ID == control.ID || copied.Name != control.Name || !copied.Required || copied.Validation != "username" ||
		copied.Placeholder != "账号" || copied.MaxLength != 64 || copied.Sort != 3 {
		t.Fatalf("incorrect checkout control copy: %+v", copied)
	}
	if d.Client.ProductSku.GetX(ctx, original.ID).PhysicalStock != 15 || got.IsLocked || got.Status != 0 {
		t.Fatal("source stock or target draft state changed")
	}

	crossTenant := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 2})
	if _, err := svc.CreateProduct(crossTenant, req); errors.Reason(err) != "catalog.COPY_SOURCE_NOT_FOUND" {
		t.Fatalf("cross-tenant copy: %v", err)
	}
	delete(permissions, "catalog:control_write")
	got, err = svc.CreateProduct(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = svc.repo.ListProductSkus(ctx, got.Id)
	if err != nil || len(rows) != 2 || d.Client.ProductControl.Query().Where(productcontrol.ProductID(got.Id)).ExistX(ctx) {
		t.Fatalf("SKU-only permission copied incorrect data: %v, %v", rows, err)
	}
	delete(permissions, "catalog:sku_read")
	if _, err := svc.CreateProduct(ctx, req); errors.Code(err) != 403 {
		t.Fatalf("copy without SKU/control permissions: %v", err)
	}
}

func TestCreateSkuReportsTheActualConflict(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("SIM").SetSlug("sim").SetGoodsType("physical").SetPhysicalStock(5).SaveX(ctx)
	for _, name := range []string{"实体卡", "eSIM卡"} {
		_, err := svc.CreateSku(ctx, &adminv1.CreateSkuRequest{ProductId: p.ID, Name: name})
		if errors.Reason(err) != "catalog.SKU_BASE_STOCK" {
			t.Fatalf("SKU %q got %v", name, err)
		}
	}
	d.Client.Product.UpdateOneID(p.ID).SetPhysicalStock(0).ExecX(ctx)
	for _, name := range []string{"实体卡", "eSIM卡"} {
		if _, err := svc.CreateSku(ctx, &adminv1.CreateSkuRequest{ProductId: p.ID, Name: name}); err != nil {
			t.Fatalf("SKU %q got %v", name, err)
		}
	}
	_, err := svc.CreateSku(ctx, &adminv1.CreateSkuRequest{ProductId: p.ID, Name: "实体卡"})
	if errors.Reason(err) != "catalog.SKU_NAME_DUPLICATE" {
		t.Fatalf("expected name conflict, got %v", err)
	}
}
