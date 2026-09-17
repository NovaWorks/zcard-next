package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"testing"
)

func TestProductVideoClearAndArchive(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	m := d.Client.Media.Create().SetName("tutorial").SetPath("tutorial.mp4").SetMime("video/mp4").SetSize(1).SetSha256("test").SaveX(ctx)
	html := `<video src="/uploads/tutorial.mp4" controls></video>`
	p, e := svc.CreateProduct(ctx, &adminv1.CreateProductRequest{Name: "video", StockType: "card", PriceCents: 100, Description: html, Status: 0})
	if e != nil {
		t.Fatal(e)
	}
	if n := d.Client.Media.GetX(ctx, m.ID).RefCount; n != 1 {
		t.Fatal(n)
	}
	_, e = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, Name: "video", StockType: "card", PriceCents: 100, Description: "", Status: 0})
	if e != nil {
		t.Fatal(e)
	}
	if v := d.Client.Product.GetX(ctx, p.Id).Description; v != "" {
		t.Fatal(v)
	}
	if n := d.Client.Media.GetX(ctx, m.ID).RefCount; n != 0 {
		t.Fatal(n)
	}
	_, e = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, Name: "video", StockType: "card", PriceCents: 100, Description: html, Status: 0})
	if e != nil {
		t.Fatal(e)
	}
	_, e = svc.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.Id})
	if e != nil {
		t.Fatal(e)
	}
	if n := d.Client.Media.GetX(ctx, m.ID).RefCount; n != 0 {
		t.Fatal(n)
	}
}
