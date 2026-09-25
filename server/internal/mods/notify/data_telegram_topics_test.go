package notify

import (
	"context"
	"encoding/json"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"net/http"
	"testing"
	"time"
)

func TestTelegramTopicsPersistAndRetryWithoutRerouting(t *testing.T) {
	ctx := context.Background()
	r := newNotifyRepo(t)
	settings := telegramSettings()
	settings.values["notify.telegram_targets"] = `[{"chat_id":"-10011","topic_id":7},{"chat_id":"-10011","topic_id":8}]`
	c := NewTelegramChannel(settings)
	calls := map[int64]int{}
	c.client = &http.Client{Transport: telegramTransport(func(req *http.Request) (*http.Response, error) {
		var payload struct {
			ChatID  string `json:"chat_id"`
			TopicID int64  `json:"message_thread_id"`
		}
		if e := json.NewDecoder(req.Body).Decode(&payload); e != nil {
			t.Fatal(e)
		}
		if payload.ChatID != "-10011" || payload.TopicID == 0 {
			t.Fatal("topic dropped", payload)
		}
		calls[payload.TopicID]++
		if payload.TopicID == 8 && calls[8] == 1 {
			return telegramResponse(429, `{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`), nil
		}
		return telegramResponse(200, `{"ok":true,"result":{"message_id":99}}`), nil
	})}
	cfg, e := c.tgConfig(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = r.enqueueTelegram(ctx, 789, "order.paid", 1, "s", "b", telegramTargets(cfg)); e != nil {
			t.Fatal(e)
		}
	}
	if n := r.data.Client.NotificationLog.Query().CountX(ctx); n != 2 {
		t.Fatal("topic dedupe", n)
	}
	d := NewDispatcher(r, c)
	if e = d.deliverTelegramDue(ctx); e != nil {
		t.Fatal(e)
	}
	r.data.Client.NotificationLog.Update().Where(notificationlog.StatusEQ(notificationlog.StatusPending)).SetNextAttemptAt(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
	if e = NewDispatcher(r, c).deliverTelegramDue(ctx); e != nil {
		t.Fatal(e)
	}
	if calls[7] != 1 || calls[8] != 2 {
		t.Fatal(calls)
	}
	// A queued notification never follows a new topic setting.
	if e = r.enqueueTelegram(ctx, 790, "order.paid", 1, "s", "b", telegramTargets(cfg)); e != nil {
		t.Fatal(e)
	}
	settings.values["notify.telegram_targets"] = `[{"chat_id":"-10011","topic_id":9}]`
	if e = d.deliverTelegramDue(ctx); e != nil {
		t.Fatal(e)
	}
	if calls[9] != 0 || calls[7] != 1 || calls[8] != 2 {
		t.Fatal("rerouted", calls)
	}
	if n := r.data.Client.NotificationLog.Query().Where(notificationlog.StatusEQ(notificationlog.StatusSkipped)).CountX(ctx); n != 2 {
		t.Fatal("not skipped", n)
	}
}

func TestTelegramSelectedTestAndPermanentTopicFailure(t *testing.T) {
	ctx := context.Background()
	r := newNotifyRepo(t)
	settings := telegramSettings()
	settings.values["notify.telegram_targets"] = `[{"chat_id":"-10011","topic_id":7},{"chat_id":"-10011","topic_id":8}]`
	c := NewTelegramChannel(settings)
	calls := 0
	c.client = &http.Client{Transport: telegramTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		var p map[string]any
		json.NewDecoder(req.Body).Decode(&p)
		if p["message_thread_id"] != float64(7) {
			t.Fatal(p)
		}
		return telegramResponse(400, `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`), nil
	})}
	d := NewDispatcher(r, c)
	svc := NewAdminNotifyService(r, d, nil)
	if _, e := svc.TestTelegram(ctx, &adminv1.TestTelegramRequest{ChatId: "-10011", TopicId: 99}); e == nil {
		t.Fatal("unsaved target accepted")
	}
	result, e := svc.TestTelegram(ctx, &adminv1.TestTelegramRequest{ChatId: "-10011", TopicId: 7})
	if e != nil || len(result.LogIds) != 1 {
		t.Fatal(result, e)
	}
	if e = d.deliverTelegramDue(ctx); e != nil {
		t.Fatal(e)
	}
	row := r.data.Client.NotificationLog.GetX(ctx, result.LogIds[0])
	if row.Status != notificationlog.StatusFailed || row.NextAttemptAt != nil || calls != 1 {
		t.Fatal("permanent failure retried or fell back", row, calls)
	}
	logs, e := svc.ListLogs(ctx, &adminv1.ListNotifyLogsRequest{Channel: "telegram"})
	if e != nil || logs.Logs[0].TopicId != 7 {
		t.Fatal(logs, e)
	}
	if _, e = svc.ResendLog(ctx, &adminv1.ResendNotifyLogRequest{Id: row.ID}); e != nil {
		t.Fatal(e)
	}
	if e = d.deliverTelegramDue(ctx); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	settings.values["notify.telegram_targets"] = `[{"chat_id":"-10011","topic_id":8}]`
	if _, e = svc.ResendLog(ctx, &adminv1.ResendNotifyLogRequest{Id: row.ID}); e == nil {
		t.Fatal("removed target retry allowed")
	}
}

func TestTelegramLegacyTargetsAndExplicitEmpty(t *testing.T) {
	ctx := context.Background()
	settings := fakeSettings{values: map[string]string{"notify.telegram": `{"enabled":true,"order_enabled":true,"bot_token":"123:legacy","chat_ids":"11,22,11"}`}}
	c := NewTelegramChannel(settings)
	cfg, e := c.tgConfig(ctx)
	if e != nil || len(telegramTargets(cfg)) != 2 {
		t.Fatal(cfg, e)
	}
	settings.values["notify.telegram_enabled"] = "true"
	settings.values["notify.telegram_order_enabled"] = "true"
	settings.values["notify.telegram_targets"] = `[{"chat_id":"-10011","topic_id":7}]`
	cfg, e = c.tgConfig(ctx)
	if e != nil || cfg.BotToken != "123:legacy" || telegramTargets(cfg)[0].TopicID != 7 {
		t.Fatal("legacy token lost", e)
	}
	settings.values["notify.telegram_targets"] = `[]`
	cfg, e = c.tgConfig(ctx)
	if e != nil || len(telegramTargets(cfg)) != 0 {
		t.Fatal("empty targets fell back", e)
	}
	settings.values["notify.telegram_targets"] = `[{"chat_id":"11","topic_id":-1}]`
	if _, e = c.tgConfig(ctx); e == nil {
		t.Fatal("invalid topic accepted")
	}
}

func TestTelegramLegacyQueuedChatDoesNotAdoptTopic(t *testing.T) {
	ctx := context.Background()
	r := newNotifyRepo(t)
	settings := telegramSettings()
	settings.values["notify.telegram_targets"] = `[{"chat_id":"11","topic_id":7}]`
	c := NewTelegramChannel(settings)
	c.client = &http.Client{Transport: telegramTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("legacy message adopted new topic")
		return nil, fmt.Errorf("unexpected")
	})}
	if e := r.enqueueTelegram(ctx, 800, "order.paid", 1, "s", "b", []notifyport.TelegramTarget{{ChatID: "11"}}); e != nil {
		t.Fatal(e)
	}
	if e := NewDispatcher(r, c).deliverTelegramDue(ctx); e != nil {
		t.Fatal(e)
	}
	if r.data.Client.NotificationLog.Query().OnlyX(ctx).Status != notificationlog.StatusSkipped {
		t.Fatal("legacy message was not stopped")
	}
}
