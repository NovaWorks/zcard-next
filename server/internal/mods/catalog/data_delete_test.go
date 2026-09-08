package catalog

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func deleteFixture(t *testing.T, status order.Status) (*data.Data, *AdminCatalogService, *ent.Product, *ent.Order, *ent.OrderItem) {
	t.Helper()
	d, s := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("旧上游商品").SetSlug("old").SetPrice(100).SetStatus(0).SetDirectContent([]byte("encrypted-content")).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("old-order").SetStatus(status).SetTotalAmount(100).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("auto").SetFulfillmentStatus("delivered").SaveX(ctx)
	return d, s, p, o, it
}
func TestDeleteProductKeepsHistoryAndBlocksResurrection(t *testing.T) {
	d, s, p, o, _ := deleteFixture(t, order.StatusCompleted)
	ctx := context.Background()
	d.Client.Product.UpdateOneID(p.ID).SetUpstreamSourceID(1).SetUpstreamProductCode("old-code").ExecX(ctx)
	ca := d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("cipher")).SetContentHash("old").SetStatus(card.StatusUsed).SetOrderID(o.ID).SaveX(ctx)
	pre, err := s.PreviewDeleteProduct(ctx, &adminv1.GetProductRequest{Id: p.ID})
	if err != nil || pre.OrderCount != 1 || pre.DeleteBlockReason != "" {
		t.Fatalf("preview %v %v", pre, err)
	}
	if d.Client.Product.GetX(ctx, p.ID).Status != 0 {
		t.Fatal("preview wrote product")
	}
	if _, err = s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, p.ID).Status != deletedProductStatus {
		t.Fatal("not archived")
	}
	if _, err = s.repo.GetAdmin(ctx, 0, p.ID); !ent.IsNotFound(err) {
		t.Fatalf("admin can still get deleted product %v", err)
	}
	rows, _, err := s.repo.ListAdmin(ctx, port.AdminFilter{})
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted product listed")
	}
	if string(d.Client.Product.GetX(ctx, p.ID).DirectContent) != "encrypted-content" || d.Client.Card.GetX(ctx, ca.ID).OrderID != o.ID {
		t.Fatal("historical fulfillment damaged")
	}
	if n, err := s.repo.BatchUpdateStatus(ctx, []uint64{p.ID}, 1); err != nil || n != 0 {
		t.Fatalf("batch revived %d %v", n, err)
	}
	if _, err := s.repo.UpdateProduct(ctx, p.ID, port.ProductInput{Name: "revive", Status: 1}); err == nil {
		t.Fatal("edit revived archive")
	}
	if n, err := s.repo.UpdateUpstreamStatus(ctx, 1, "old-code", 1); err != nil || n {
		t.Fatal("sync revived archive")
	}
	if _, _, err := s.repo.UpsertUpstreamProduct(ctx, port.UpstreamProductInput{ConnectionID: 1, UpstreamProductCode: "old-code", Name: "revive", Status: 1}); err == nil {
		t.Fatal("full sync revived archive")
	}
	if _, err := s.repo.ShelveOffMissing(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, p.ID).Status != deletedProductStatus {
		t.Fatal("missing sync revived")
	}
}
func TestDeleteProductPurgesOrdersAndKeepsPaymentAnchor(t *testing.T) {
	d, s, p, o, it := deleteFixture(t, order.StatusCompleted)
	ctx := context.Background()
	ca := d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("cipher")).SetContentHash("old").SetStatus(card.StatusUsed).SetOrderID(o.ID).SaveX(ctx)
	d.Client.OrderDelivery.Create().SetOrderID(o.ID).SetItemID(it.ID).SetCardID(ca.ID).SetDeliveredMode("status").SetDeliveryTokenHash("token").SaveX(ctx)
	pay := d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("test").SetAmount(100).SetStatus("success").SetChannelOrderNo("upstream-paid").SaveX(ctx)
	d.Client.OrderAmountLine.Create().SetOrderID(o.ID).SetType("base_price").SetAmount(100).SaveX(ctx)
	d.Client.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus("paid").SetToStatus("completed").SetEvent("completed").SetOperator("system").SaveX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(0).SetChannel("gateway").SetStatus("succeeded").SaveX(ctx)
	proc := d.Client.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(1).SetDedupeKey("old-order").SetStatus("fulfilled").SaveX(ctx)
	d.Client.ProcurementItem.Create().SetProcurementID(proc.ID).SetQuantity(1).SetUnitCost(50).SaveX(ctx)
	req := &adminv1.DeleteProductRequest{Id: p.ID, DeleteOrders: true, ConfirmName: p.Name, ExpectedOrderCount: 2}
	if _, err := s.DeleteProduct(ctx, req); err == nil {
		t.Fatal("stale count accepted")
	}
	if d.Client.Order.Query().CountX(ctx) != 1 || d.Client.Product.GetX(ctx, p.ID).Status != 0 {
		t.Fatal("failure not rolled back")
	}
	req.ExpectedOrderCount = 1
	req.ConfirmName = "wrong"
	if _, err := s.DeleteProduct(ctx, req); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	req.ConfirmName = p.Name
	if _, err := s.DeleteProduct(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d.Client.Order.Query().CountX(ctx) != 0 || d.Client.OrderItem.Query().CountX(ctx) != 0 || d.Client.OrderDelivery.Query().CountX(ctx) != 0 || d.Client.Card.Query().CountX(ctx) != 0 || d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.ProcurementOrder.Query().CountX(ctx) != 0 || d.Client.ProcurementItem.Query().CountX(ctx) != 0 || d.Client.OrderAmountLine.Query().CountX(ctx) != 0 || d.Client.OrderStatusEvent.Query().CountX(ctx) != 0 {
		t.Fatal("order children left behind")
	}
	if got := d.Client.Payment.GetX(ctx, pay.ID); got.OrderID != 0 || got.ChannelOrderNo != "upstream-paid" || got.Status != "success" {
		t.Fatal("payment anchor corrupted")
	}
}
func TestDeleteProductGuards(t *testing.T) {
	cases := []struct {
		name   string
		modify func(context.Context, *data.Data, *ent.Product, *ent.Order)
	}{
		{"onshelf", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.Product.UpdateOneID(p.ID).SetStatus(1).ExecX(c)
		}},
		{"hidden", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.Product.UpdateOneID(p.ID).SetStatus(2).ExecX(c)
		}},
		{"foreign", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.Product.UpdateOneID(p.ID).SetSubsiteID(99).ExecX(c)
		}},
		{"unpaid", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.Order.UpdateOneID(o.ID).SetStatus(order.StatusPendingPayment).ExecX(c)
		}},
		{"fulfilling", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.Order.UpdateOneID(o.ID).SetStatus(order.StatusFulfilling).ExecX(c)
		}},
		{"refund", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(100).SetChannel("gateway").SetStatus("processing").SaveX(c)
		}},
		{"stock", func(c context.Context, d *data.Data, p *ent.Product, o *ent.Order) {
			d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("cipher")).SetContentHash("unused").SetStatus(card.StatusAvailable).SaveX(c)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			d, s, p, o, _ := deleteFixture(t, order.StatusCompleted)
			ctx := context.Background()
			tt.modify(ctx, d, p, o)
			for _, purge := range []bool{false, true} {
				if _, err := s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID, DeleteOrders: purge, ConfirmName: p.Name, ExpectedOrderCount: 1}); err == nil {
					t.Fatal("unsafe deletion allowed")
				}
			}
			if d.Client.Order.Query().CountX(ctx) != 1 {
				t.Fatal("blocked deletion removed order")
			}
		})
	}
}
func TestDeleteProductMixedOrderOnlyAllowsKeepingOrders(t *testing.T) {
	d, s, p, o, _ := deleteFixture(t, order.StatusCompleted)
	ctx := context.Background()
	p2 := d.Client.Product.Create().SetName("other").SetSlug("other").SetPrice(1).SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p2.ID).SetQuantity(1).SetUnitPrice(1).SetAmount(1).SetFulfillmentType("auto").SaveX(ctx)
	pre, err := s.PreviewDeleteProduct(ctx, &adminv1.GetProductRequest{Id: p.ID})
	if err != nil || pre.DeleteOrdersBlockReason == "" || pre.DeleteBlockReason != "" {
		t.Fatalf("preview %v %v", pre, err)
	}
	if _, err = s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID, DeleteOrders: true, ConfirmName: p.Name, ExpectedOrderCount: 1}); err == nil {
		t.Fatal("mixed order deleted")
	}
	if _, err = s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); err != nil {
		t.Fatal(err)
	}
	if d.Client.OrderItem.Query().CountX(ctx) != 2 {
		t.Fatal("mixed order damaged")
	}
}

func TestDeleteProductHTTPQueryConfirmation(t *testing.T) {
	d, s, p, _, _ := deleteFixture(t, order.StatusCompleted)
	server := kratoshttp.NewServer()
	adminv1.RegisterAdminCatalogServiceHTTPServer(server, s)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/admin/products/%d/delete-preview", p.ID), nil))
	if w.Code != 200 {
		t.Fatalf("preview HTTP %d %s", w.Code, w.Body.String())
	}
	params := url.Values{"delete_orders": {"true"}, "confirm_name": {p.Name}, "expected_order_count": {"1"}}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/admin/products/%d?%s", p.ID, params.Encode()), nil))
	if w.Code != 200 {
		t.Fatalf("delete HTTP %d %s", w.Code, w.Body.String())
	}
	if d.Client.Order.Query().CountX(context.Background()) != 0 {
		t.Fatal("query fields not bound; orders still present")
	}
}
