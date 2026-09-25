package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

type telegramTransport func(*http.Request) (*http.Response, error)

func (f telegramTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func telegramResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func telegramSettings() fakeSettings {
	return fakeSettings{values: map[string]string{
		"notify.telegram_enabled": "true", "notify.telegram_order_enabled": "true", "notify.telegram_bot_token": `"123:secret"`, "notify.telegram_chat_ids": `"11,22,11"`, "notify.telegram_events": `["order.paid"]`,
	}}
}
func TestTelegramAPIResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		permanent  bool
		retry      time.Duration
	}{
		{"json failure", `{"ok":false,"error_code":403,"description":"blocked"}`, 200, true, 0},
		{"rate limited", `{"ok":false,"error_code":429,"parameters":{"retry_after":120}}`, 429, false, 120 * time.Second},
		{"server", `{"ok":false,"error_code":500}`, 500, false, 0},
		{"malformed", `<html>bad gateway</html>`, 502, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewTelegramChannel(telegramSettings())
			c.client = &http.Client{Transport: telegramTransport(func(*http.Request) (*http.Response, error) { return telegramResponse(tc.status, tc.body), nil })}
			_, err := c.sendResult(context.Background(), "123:secret", "11", "test")
			var e *TelegramError
			if !errors.As(err, &e) || e.Permanent != tc.permanent || e.RetryAfter != tc.retry {
				t.Fatalf("unexpected: %v", err)
			}
		})
	}
	c := NewTelegramChannel(telegramSettings())
	c.client = &http.Client{Transport: telegramTransport(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("test failure") })}
	_, err := c.sendResult(context.Background(), "123:secret", "11", "test")
	if err == nil || strings.Contains(err.Error(), "123:secret") || strings.Contains(err.Error(), "api.telegram.org/bot") {
		t.Fatalf("leaked URL: %v", err)
	}
}
func TestTelegramPersistentDelivery(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	settings := telegramSettings()
	c := NewTelegramChannel(settings)
	calls := map[string]int{}
	c.client = &http.Client{Transport: telegramTransport(func(req *http.Request) (*http.Response, error) {
		var payload map[string]string
		_ = json.NewDecoder(req.Body).Decode(&payload)
		id := payload["chat_id"]
		calls[id]++
		if id == "22" && calls[id] == 1 {
			return telegramResponse(429, `{"ok":false,"error_code":429,"parameters":{"retry_after":120}}`), nil
		}
		return telegramResponse(200, `{"ok":true,"result":{"message_id":77}}`), nil
	})}
	d := NewDispatcher(r, c)
	for i := 0; i < 2; i++ {
		if err := r.enqueueTelegram(ctx, 123, events.OrderPaid, 1, "paid", "body", []notifyport.TelegramTarget{{ChatID: "11"}, {ChatID: "22"}}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := r.data.Client.NotificationLog.Query().Count(ctx); n != 2 {
		t.Fatal("duplicate events", n)
	}
	if err := d.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := r.data.Client.NotificationLog.Query().All(ctx)
	for _, row := range rows {
		if row.Recipient == "11" && (row.Status != notificationlog.StatusSent || row.MessageID != "77" || row.Attempts != 1) {
			t.Fatal(row)
		}
		if row.Recipient == "22" && (row.Status != notificationlog.StatusPending || row.NextAttemptAt == nil || time.Until(*row.NextAttemptAt) < 110*time.Second) {
			t.Fatal(row)
		}
	}
	// Restart dispatcher: only the failed destination is retried.
	_, _ = r.data.Client.NotificationLog.Update().Where(notificationlog.StatusEQ(notificationlog.StatusPending)).SetNextAttemptAt(time.Now().UTC().Add(-time.Minute)).Save(ctx)
	if err := NewDispatcher(r, c).deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if calls["11"] != 1 || calls["22"] != 2 {
		t.Fatal(calls)
	}
	// Active lease excludes a job; expired lease recovers it after a crash.
	_ = r.enqueueTelegram(ctx, 124, events.OrderPaid, 1, "paid", "body", []notifyport.TelegramTarget{{ChatID: "11"}})
	_, _ = r.data.Client.NotificationLog.Update().Where(notificationlog.StatusEQ(notificationlog.StatusPending)).SetLeaseUntil(time.Now().UTC().Add(time.Minute)).Save(ctx)
	_ = d.deliverTelegramDue(ctx)
	if calls["11"] != 1 {
		t.Fatal("active lease ignored")
	}
	_, _ = r.data.Client.NotificationLog.Update().Where(notificationlog.StatusEQ(notificationlog.StatusPending)).SetLeaseUntil(time.Now().UTC().Add(-time.Minute)).Save(ctx)
	_ = d.deliverTelegramDue(ctx)
	if calls["11"] != 2 {
		t.Fatal("expired lease not recovered")
	}
	// Disabling stops queued deliveries.
	_ = r.enqueueTelegram(ctx, 125, events.OrderPaid, 1, "paid", "body", []notifyport.TelegramTarget{{ChatID: "11"}})
	settings.values["notify.telegram_order_enabled"] = "false"
	_ = d.deliverTelegramDue(ctx)
	if calls["11"] != 2 {
		t.Fatal("sent while disabled")
	}
	if n, _ := r.data.Client.NotificationLog.Query().Where(notificationlog.StatusEQ(notificationlog.StatusSkipped)).Count(ctx); n != 1 {
		t.Fatal("missing skipped log")
	}
	// A rolled-back event may not leave deliverable tasks.
	err := data.Tx(ctx, r.data, func(tx context.Context) error {
		if e := r.enqueueTelegram(tx, 126, events.OrderPaid, 1, "paid", "body", []notifyport.TelegramTarget{{ChatID: "11"}}); e != nil {
			return e
		}
		return fmt.Errorf("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	if n, _ := r.data.Client.NotificationLog.Query().Where(notificationlog.DeliveryKey("telegram:126:11")).Count(ctx); n != 0 {
		t.Fatal("rollback left job")
	}
}
func TestTelegramTenantIsolation(t *testing.T) {
	r := newNotifyRepo(t)
	d := NewDispatcher(r, NewTelegramChannel(telegramSettings()))
	if err := d.EnqueueTelegram(context.Background(), events.Envelope{SubsiteID: 42, Type: events.OrderPaid, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.data.Client.NotificationLog.Query().Count(context.Background()); n != 0 {
		t.Fatal("tenant leaked")
	}
}

func TestTelegramOrderMessageAndSnapshot(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	settings := telegramSettings()
	settings.values["site.name"] = `"商店 <安全>"`
	settings.values["site.url"] = `"https://shop.example"`
	c := NewTelegramChannel(settings)
	d := NewDispatcher(r, c)
	d.adminPath = func(context.Context) (string, error) { return "/private-admin", nil }
	o, err := r.data.Client.Order.Create().SetOrderNo("ORDER-TG").SetBaseCurrency("CNY").SetTotalAmount(1000).SetContact("private@example.com").SetQueryPasswordHash("private-password").SetExpiredAt(time.Now().UTC().Add(time.Hour)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.data.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetProductName("Service <b>&").SetSkuName("SKU").SetQuantity(2).SetUnitPrice(500).SetAmount(1000).SetCost(0).SetFulfillmentType("manual").SetFormAnswers([]map[string]string{{"secret": "private-form"}}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.data.Client.Payment.Create().SetOrderID(o.ID).SetChannel("balance").SetAmount(1000).SetStatus("success").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(fmt.Sprintf(`{"order_id":%d}`, o.ID))
	ev, err := r.data.Client.OutboxEvent.Create().SetModule("order").SetType(events.OrderPaid).SetAggregateID(o.OrderNo).SetPayload(payload).SetDedupeKey("test-paid").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	env := events.Envelope{EventID: ev.ID, Type: events.OrderPaid, AggregateID: o.OrderNo, Payload: payload}
	for i := 0; i < 2; i++ {
		if err = data.Tx(ctx, r.data, func(tx context.Context) error { return d.EnqueueTelegram(tx, env) }); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.data.Client.NotificationLog.Query().All(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("jobs: %d %v", len(rows), err)
	}
	for _, row := range rows {
		for _, want := range []string{"CNY 10.00", "&lt;b&gt;&amp;", "× 2", "人工服务", "/private-admin/order?order_no=ORDER-TG"} {
			if !strings.Contains(row.Body, want) {
				t.Fatalf("missing %q: %s", want, row.Body)
			}
		}
		for _, secret := range []string{"private@example.com", "private-password", "private-form"} {
			if strings.Contains(row.Body, secret) {
				t.Fatal("private data leaked")
			}
		}
	}
}
