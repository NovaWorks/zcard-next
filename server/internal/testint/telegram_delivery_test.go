//go:build integration

package testint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

type telegramFixture map[string]string

func (f telegramFixture) GetJSON(_ context.Context, group, key string) ([]byte, error) {
	return []byte(f[group+"."+key]), nil
}
func TestTelegramDeliveryMySQL(t *testing.T) { runTelegramDelivery(MySQL(t)) }
func TestTelegramDeliveryPG(t *testing.T)    { runTelegramDelivery(PG(t)) }
func runTelegramDelivery(h *Harness) {
	t := h.T
	ctx := context.Background()
	c := h.Data.Client
	cfg := telegramFixture{"notify.telegram_enabled": "true", "notify.telegram_order_enabled": "true", "notify.telegram_bot_token": `"fixture"`, "notify.telegram_chat_ids": `"123"`, "notify.telegram_events": `["order.paid"]`}
	d := notify.NewDispatcher(notify.NewNotifyRepo(h.Data), notify.NewTelegramChannel(cfg))
	o := c.Order.Create().SetOrderNo("ORDER-TG").SetBaseCurrency("CNY").SetTotalAmount(100).SetExpiredAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	payload, _ := json.Marshal(map[string]any{"order_id": o.ID})
	event := c.OutboxEvent.Create().SetModule("order").SetType(events.OrderPaid).SetAggregateID(o.OrderNo).SetDedupeKey("tg-paid").SetPayload(payload).SaveX(ctx)
	env := events.Envelope{EventID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: payload}
	for i := 0; i < 2; i++ {
		if err := data.Tx(ctx, h.Data, func(tx context.Context) error { return d.EnqueueTelegram(tx, env) }); err != nil {
			t.Fatal(err)
		}
	}
	rows := c.NotificationLog.Query().AllX(ctx)
	if len(rows) != 1 {
		t.Fatal("duplicate task", len(rows))
	}
	// Scanner must handle real DB timestamp precision and skip after disabling. No network call.
	cfg["notify.telegram_order_enabled"] = "false"
	d.ScanTelegram(ctx)
	row := c.NotificationLog.GetX(ctx, rows[0].ID)
	if row.Status != notificationlog.StatusSkipped || row.LeaseUntil != nil || row.Attempts != 1 {
		t.Fatalf("lease/status mismatch: %+v", row)
	}
	// Existing logs have no delivery key; null uniqueness allows more than one historical row.
	for i := 0; i < 2; i++ {
		c.NotificationLog.Create().SetEventType("old").SetChannel("telegram").SetRecipient("123").SetStatus("sent").ExecX(ctx)
	}
}
