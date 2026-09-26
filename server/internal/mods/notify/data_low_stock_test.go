package notify

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"strings"
	"testing"
	"time"
)

func TestLowStockNotificationsAreIndependentAndDeduplicated(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	c := r.data.Client
	settings := telegramSettings()
	settings.values["notify.telegram_order_enabled"] = "false"
	settings.values["notify.telegram_low_stock_enabled"] = "true"
	settings.values["supply.low_stock_threshold"] = "5"
	channel := NewTelegramChannel(settings)
	d := NewDispatcher(r, channel)
	p := c.Product.Create().SetName("<unsafe>").SetSlug("low").SetStatus(1).SetUpstreamSourceID(9).SetUpstreamProductCode("p").SaveX(ctx)
	m := c.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetUpstreamProduct("p").SetUpStock(4).SetStockCheckedAt(time.Now()).SaveX(ctx)
	scan := func() {
		t.Helper()
		if err := d.scanLowStock(ctx); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	scan()
	if n := c.NotificationLog.Query().CountX(ctx); n != 2 {
		t.Fatalf("want one digest per target got %d", n)
	}
	row := c.NotificationLog.Query().FirstX(ctx)
	if !strings.Contains(row.Body, "&lt;unsafe&gt;") || row.BizType != "stock" {
		t.Fatal(row)
	}
	// Unknown must neither trigger nor recover the existing episode.
	c.SupplyMapping.UpdateOneID(m.ID).SetUpStock(-2).ExecX(ctx)
	scan()
	c.SupplyMapping.UpdateOneID(m.ID).SetUpStock(3).SetStockCheckedAt(time.Now()).ExecX(ctx)
	scan()
	if c.NotificationLog.Query().CountX(ctx) != 2 {
		t.Fatal("unknown incorrectly reset state")
	}
	c.SupplyMapping.UpdateOneID(m.ID).SetUpStock(0).ExecX(ctx)
	scan()
	scan()
	if c.NotificationLog.Query().CountX(ctx) != 4 {
		t.Fatal("zero-stock escalation missing or duplicated")
	}
	c.SupplyMapping.UpdateOneID(m.ID).SetUpStock(5).ExecX(ctx)
	scan() // equality recovers
	c.SupplyMapping.UpdateOneID(m.ID).SetUpStock(2).ExecX(ctx)
	scan()
	if c.NotificationLog.Query().CountX(ctx) != 4 {
		t.Fatal("cooldown ignored")
	}
	c.StockAlert.Update().SetLastNotifiedAt(time.Now().Add(-31 * time.Minute).Unix()).ExecX(ctx)
	scan()
	if c.NotificationLog.Query().CountX(ctx) != 6 {
		t.Fatal("recovered episode was not rearmed")
	}
	settings.values["notify.telegram_low_stock_enabled"] = "false"
	if err := d.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if c.NotificationLog.Query().Where(notificationlog.StatusEQ(notificationlog.StatusSkipped)).CountX(ctx) != 6 {
		t.Fatal("disabled notifications still sent")
	}
}
func TestLowStockNotificationsRespectSKUAndStaleness(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	c := r.data.Client
	settings := telegramSettings()
	settings.values["notify.telegram_low_stock_enabled"] = "true"
	d := NewDispatcher(r, NewTelegramChannel(settings))
	p := c.Product.Create().SetName("multi").SetSlug("multi").SetStatus(1).SetUpstreamSourceID(9).SetUpstreamProductCode("p").SaveX(ctx)
	for _, entry := range []struct {
		name string
		n    int32
		age  time.Duration
	}{{"scarce", 2, 0}, {"full", 100, 0}, {"stale", 0, -10 * time.Minute}} {
		sku := c.ProductSku.Create().SetProductID(p.ID).SetName(entry.name).SetSpecValues(map[string]string{}).SetUpstreamSkuID(entry.name).SaveX(ctx)
		c.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetLocalSkuID(sku.ID).SetUpstreamProduct("p").SetUpstreamSku(entry.name).SetUpStock(entry.n).SetStockCheckedAt(time.Now().Add(entry.age)).SaveX(ctx)
	}
	if err := d.scanLowStock(ctx); err != nil {
		t.Fatal(err)
	}
	row := c.NotificationLog.Query().FirstX(ctx)
	if !strings.Contains(row.Body, "scarce") || strings.Contains(row.Body, "stale") || strings.Contains(row.Body, "full") {
		t.Fatal(row.Body)
	}
	if c.StockAlert.Query().CountX(ctx) != 2 {
		t.Fatal("stale SKU created alert")
	}
}
func TestLowStockLocalCardsAndDigest(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	c := r.data.Client
	settings := telegramSettings()
	settings.values["notify.telegram_low_stock_enabled"] = "true"
	d := NewDispatcher(r, NewTelegramChannel(settings))
	for _, name := range []string{"a", "b", "c"} {
		c.Product.Create().SetName(name).SetSlug(name).SetStatus(1).SetStockType("card").SaveX(ctx)
	}
	if err := d.scanLowStock(ctx); err != nil {
		t.Fatal(err)
	}
	if c.NotificationLog.Query().CountX(ctx) != 2 {
		t.Fatal("initial low stock not digested")
	}
	if !strings.Contains(c.NotificationLog.Query().FirstX(ctx).Body, "已售罄") {
		t.Fatal("missing local zero stock")
	}
}
