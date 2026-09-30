package supply

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"testing"
)

type pagedSMSCatalog struct {
	adapter.Adapter
	calls int
}

func (a *pagedSMSCatalog) ListProducts(_ context.Context, page, size int, _ bool) (*adapter.ProductList, error) {
	a.calls++
	return &adapter.ProductList{PageSize: size, Total: 100001, HasMore: page < 2001, Items: []adapter.Product{{ID: "opaque", Name: "接码", CategoryID: "sms-wa", Price: 123, Stock: 5, IsActive: true, DeliveryKind: "sms_activation", SMSProduct: map[string]string{"platform_id": "platform"}}}}, nil
}
func (a *pagedSMSCatalog) ListCategories(context.Context) ([]adapter.Category, error) {
	return []adapter.Category{{ID: "card", Name: "卡密"}, {ID: "card-wa", Name: "WhatsApp", ParentID: "card"}, {ID: "sms", Name: "短信接码"}, {ID: "sms-wa", Name: "WhatsApp", ParentID: "sms"}}, nil
}
func TestSMSCatalogLargePageAndHierarchy(t *testing.T) {
	s, c, _ := catalogFixture(t)
	a := &pagedSMSCatalog{}
	attachCatalogAdapter(s, a)
	ctx := context.Background()
	got, e := s.PreviewProducts(ctx, &adminv1.PreviewProductsRequest{ConnectionId: c.ID, Page: 2001, PageSize: 50})
	if e != nil {
		t.Fatal(e)
	}
	if a.calls != 1 || got.Total != 100001 || got.Page != 2001 || got.HasMore || len(got.Categories) != 1 || got.Categories[0].Path != "短信接码 / WhatsApp" {
		t.Fatalf("bad page: %+v", got)
	}
	if _, e = s.PreviewProducts(ctx, &adminv1.PreviewProductsRequest{ConnectionId: c.ID, SnapshotId: got.SnapshotId}); e != nil || a.calls != 1 {
		t.Fatal("page snapshot refetched", e)
	}
	tree, _ := a.ListCategories(ctx)
	nodes := map[string]adapter.Category{}
	for _, n := range tree {
		nodes[n.ID] = n
	}
	mapped := map[string]uint64{}
	client := s.repo.entClient(ctx)
	card, e := ensurePageCategory(ctx, client, 0, "card-wa", nodes, mapped, "")
	if e != nil {
		t.Fatal(e)
	}
	sms, e := ensurePageCategory(ctx, client, 0, "sms-wa", nodes, mapped, "")
	if e != nil {
		t.Fatal(e)
	}
	if card == sms || client.Category.Query().Where(category.Name("WhatsApp")).CountX(ctx) != 2 {
		t.Fatal("same leaf collapsed across roots")
	}
	again, e := ensurePageCategory(ctx, client, 0, "sms-wa", nodes, map[string]uint64{}, "")
	if e != nil || again != sms {
		t.Fatal("hierarchy import not idempotent", e)
	}
}
func TestSMSCatalogRejectsBrokenHierarchy(t *testing.T) {
	for _, tree := range [][]adapter.Category{{{ID: "a", ParentID: "b"}}, {{ID: "a", ParentID: "b"}, {ID: "b", ParentID: "a"}}, {{ID: "a"}, {ID: "a"}}} {
		if _, e := categoryPaths(tree); e == nil {
			t.Fatal("invalid tree accepted")
		}
	}
}
func TestSMSActiveIntentProtectsAccountIdentity(t *testing.T) {
	r, d := newTestRepo(t)
	c := mustConn(t, r, d, "original")
	ctx := context.Background()
	d.Client.SMSIntent.Create().SetOrderID(1).SetOrderItemID(1).SetUserID(1).SetConnectionID(c.ID).SetConnectionIdentity("original").SetRequestNo("sms_fixed").SetRequestJSON("{}").SetRequestHash("hash").SaveX(ctx)
	if _, e := r.UpdateConnection(ctx, c.ID, &ConnectionUpdate{SupplyConnection: &ent.SupplyConnection{BaseURL: "https://changed.example"}}); e == nil {
		t.Fatal("changed URL while outstanding")
	}
	if e := r.UpdateCredentials(ctx, c.ID, "zcard", c.BaseURL, `{"api_key":"new","api_secret":"new"}`); e == nil {
		t.Fatal("changed account while outstanding")
	}
	if e := r.DeleteConnection(ctx, c.ID); e == nil {
		t.Fatal("deleted outstanding account")
	}
	if _, e := r.UpdateConnection(ctx, c.ID, &ConnectionUpdate{SupplyConnection: &ent.SupplyConnection{Name: "renamed"}}); e != nil {
		t.Fatal("non-identity edit denied", e)
	}
}
