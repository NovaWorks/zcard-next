//go:build integration

package testint

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/dashboard"
)

func TestAdminLoadingPG(t *testing.T)    { runAdminLoading(PG(t)) }
func TestAdminLoadingMySQL(t *testing.T) { runAdminLoading(MySQL(t)) }
func runAdminLoading(h *Harness) {
	ctx, c, t := context.Background(), h.Data.Client, h.T
	for i := 0; i < 107; i++ {
		c.Product.Create().SetName(fmt.Sprintf("option-%d", i)).SetSlug(fmt.Sprintf("option-%d", i)).SetPrice(123).SetDescription("description").SetStockType("url").SetStatus(1).SaveX(ctx)
	}
	svc := catalog.NewAdminCatalogService(catalog.NewProductRepoImpl(h.Data, nil), nil, nil, nil)
	reply, err := svc.ListProducts(ctx, &adminv1.ListProductsRequest{OptionsOnly: true, Page: 2, PageSize: 100})
	if err != nil || reply.Total != 107 || len(reply.Products) != 7 || reply.Products[0].Description != "" || reply.Products[0].PriceCents != 123 {
		t.Fatalf("options=%+v err=%v", reply, err)
	}
	legacy, err := svc.ListProducts(ctx, &adminv1.ListProductsRequest{PageSize: 1})
	if err != nil || legacy.Products[0].Description != "description" || legacy.Products[0].Stock != -1 {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	statuses := []order.Status{order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted, order.StatusPendingPayment, order.StatusCanceled, order.StatusRefunded}
	for i, status := range statuses {
		b := c.Order.Create().SetOrderNo(fmt.Sprintf("metric-%d", i)).SetSubsiteID(0).SetStatus(status).SetTotalAmount(101).SetCost(31).SetCreatedAt(now)
		if status != order.StatusPendingPayment && status != order.StatusCanceled {
			b.SetPaidAt(now)
		}
		o := b.SaveX(ctx)
		if status == order.StatusRefunded {
			c.RefundOrder.Create().SetOrderID(o.ID).SetChannel("wallet").SetAmount(101).SetStatus("succeeded").SetCreatedAt(now).SaveX(ctx)
		}
	}
	c.Order.Create().SetOrderNo("other-tenant").SetSubsiteID(9).SetStatus(order.StatusPaid).SetTotalAmount(99999).SetCreatedAt(now).SetPaidAt(now).SaveX(ctx)
	repo := dashboard.NewDashboardRepoImpl(h.Data)
	_, _, week, _, _, _, err := repo.GetOverview(ctx)
	if err != nil || week.Orders != 8 || week.PaidOrders != 6 || week.Revenue != 606 || week.Cost != 186 || week.Refunds != 101 || week.NetRevenue != 505 || week.Profit != 319 {
		t.Fatalf("metric=%+v err=%v", week, err)
	}
}
