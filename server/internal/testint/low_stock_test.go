//go:build integration

package testint

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestLowStockMySQL(t *testing.T) { runLowStock(MySQL(t)) }
func TestLowStockPG(t *testing.T)    { runLowStock(PG(t)) }
func runLowStock(h *Harness) {
	t, ctx, c := h.T, context.Background(), h.Data.Client
	cfg := telegramFixture{"notify.telegram_enabled": "true", "notify.telegram_low_stock_enabled": "true", "notify.telegram_order_enabled": "false", "notify.telegram_bot_token": `"fixture"`, "notify.telegram_targets": `[{"chat_id":"123","topic_id":7}]`, "supply.low_stock_threshold": "5"}
	d := notify.NewDispatcher(notify.NewNotifyRepo(h.Data), notify.NewTelegramChannel(cfg))
	p := c.Product.Create().SetName("low stock").SetSlug("low-stock").SetStatus(1).SetUpstreamSourceID(9).SetUpstreamProductCode("p").SaveX(ctx)
	m := c.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetUpstreamProduct("p").SetUpStock(2).SetStockCheckedAt(time.Now().UTC()).SaveX(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); d.ScanLowStock(ctx) }()
	}
	wg.Wait()
	if n := c.NotificationLog.Query().CountX(ctx); n != 1 {
		t.Fatalf("expected 1 alert after concurrent scans: %d", n)
	}
	row := c.NotificationLog.Query().FirstX(ctx)
	if row.Variables["telegram_topic_id"] != "7" || row.EventType != "stock.low" {
		t.Fatal(row)
	}
	// A no-op alert update must also work with MySQL's zero affected-row semantics.
	d.ScanLowStock(ctx)
	if c.NotificationLog.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate")
	}
	c.SupplyMapping.UpdateOneID(m.ID).SetUpStock(0).SetStockCheckedAt(time.Now().UTC()).ExecX(ctx)
	d.ScanLowStock(ctx)
	if c.NotificationLog.Query().CountX(ctx) != 2 {
		t.Fatal("zero-stock escalation missing")
	}
	cfg["notify.telegram_low_stock_enabled"] = "false"
	d.ScanTelegram(ctx)
	if c.NotificationLog.Query().Where(notificationlog.StatusEQ(notificationlog.StatusSkipped)).CountX(ctx) != 2 {
		t.Fatal("disabled delivery was not skipped")
	}
}

// Exercise the mapping claim and correlated SKU predicates on both real dialects.
// Invalid fixture credentials stop before any network request.
func TestLowStockProbeMySQL(t *testing.T) { runLowStockProbe(MySQL(t)) }
func TestLowStockProbePG(t *testing.T)    { runLowStockProbe(PG(t)) }
func runLowStockProbe(h *Harness) {
	t, ctx, c := h.T, context.Background(), h.Data.Client
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := supply.NewSupplyRepoImpl(h.Data, box)
	svc := supply.NewSyncService(repo, nil, nil, nil, nil, nil, slog.Default())
	conn := c.SupplyConnection.Create().SetName("probe").SetDriver("zcard").SetBaseURL("https://example.invalid").SetCredentials([]byte("invalid fixture")).SetSettings(map[string]any{"schedule": map[string]any{"low_stock": map[string]any{"enabled": true, "threshold": 10, "interval": 5}}}).SaveX(ctx)
	p := c.Product.Create().SetName("probe").SetSlug("probe").SetStatus(1).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("probe").SaveX(ctx)
	sk := c.ProductSku.Create().SetProductID(p.ID).SetName("a").SetSpecValues(map[string]string{}).SetUpstreamSkuID("a").SaveX(ctx)
	m := c.SupplyMapping.Create().SetConnectionID(conn.ID).SetLocalProductID(p.ID).SetLocalSkuID(sk.ID).SetUpstreamProduct("probe").SetUpstreamSku("a").SetUpStock(2).SetStockCheckedAt(time.Now().Add(-10 * time.Minute)).SaveX(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); svc.ScanLowStock(ctx) }()
	}
	wg.Wait()
	row := c.SupplyMapping.GetX(ctx, m.ID)
	if row.UpStock != -2 || row.StockProbeFailures != 1 || row.StockProbeLease != 0 || row.StockProbeAfter <= time.Now().Unix() {
		t.Fatalf("probe claim/retry failed: %+v", row)
	}
}
