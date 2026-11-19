package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
)

func TestUpstreamFormSyncPreservesKeysAndRollsBackConflicts(t *testing.T) {
	d, repo := newMaintainEnv(t)
	ctx := context.Background()
	fields, err := purchaseform.ParseSchema(json.RawMessage(`{"fields":[{"key":"handle","type":"text","label":{"zh-CN":"用户名"},"required":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	in := port.UpstreamProductInput{ConnectionID: 9, UpstreamProductCode: "form", Name: "Supplier form", Price: 100, Status: 1, FormFields: fields}
	id, _, err := repo.UpsertUpstreamProduct(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	control := d.Client.ProductControl.Query().Where(productcontrol.ProductID(id)).OnlyX(ctx)
	first := d.Client.Product.GetX(ctx, id)
	if control.Name != "用户名" || !control.Required {
		t.Fatal("supplier controls were not imported")
	}
	if _, _, err := repo.UpsertUpstreamProduct(ctx, in); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, id).LockVersion != first.LockVersion {
		t.Fatal("unchanged sync invalidated active forms")
	}
	in.FormFields[0].Label = "客户账号"
	if _, _, err := repo.UpsertUpstreamProduct(ctx, in); err != nil {
		t.Fatal(err)
	}
	if d.Client.ProductControl.GetX(ctx, control.ID).Name != "客户账号" {
		t.Fatal("field identity was not preserved across label changes")
	}
	manual := d.Client.ProductControl.Create().SetProductID(id).SetName("手工字段").SetType("text").SaveX(ctx)
	in.Name = "must roll back"
	if _, _, err := repo.UpsertUpstreamProduct(ctx, in); err == nil {
		t.Fatal("local control conflict accepted")
	}
	if d.Client.Product.GetX(ctx, id).Name != "Supplier form" {
		t.Fatal("failed sync partially changed the product")
	}
	in.FormFields = []purchaseform.Field{}
	if _, _, err := repo.UpsertUpstreamProduct(ctx, in); err != nil {
		t.Fatal(err)
	}
	if d.Client.ProductControl.Query().Where(productcontrol.ID(control.ID)).ExistX(ctx) || !d.Client.ProductControl.Query().Where(productcontrol.ID(manual.ID)).ExistX(ctx) {
		t.Fatal("sync removed the wrong owner's controls")
	}
}
