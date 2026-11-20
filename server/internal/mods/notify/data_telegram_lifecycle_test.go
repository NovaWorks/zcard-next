package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/outboxevent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
	settingsmod "github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
)

func TestTelegramManualOrderLifecycle(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	d := r.data
	p := d.Client.Product.Create().SetName("人工商品").SetSlug("manual-tg").SetPrice(1000).SetStatus(1).SetFulfillmentMode("manual").SetManualStock(10).SaveX(ctx)
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	uc := &ordermod.OrderUsecase{Data: d, Gen: gen, Outbox: data.NewOutboxWriter(d)}
	settings := telegramSettings()
	settings.values["notify.telegram_events"] = `["order.created","order.paid","order.delivered"]`
	settings.values["notify.telegram_chat_ids"] = `"11"`
	channel := NewTelegramChannel(settings)
	var sent []string
	channel.client = &http.Client{Transport: telegramTransport(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sent = append(sent, body["text"].(string))
		return telegramResponse(200, `{"ok":true,"result":{"message_id":9}}`), nil
	})}
	notifications := NewDispatcher(r, channel)
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	delivery := fulfillment.NewDeliveryRepoImpl(d, cipher, nil, nil)
	bus := data.NewDispatcher(d, nil)
	notifications.RegisterTelegramOrders(bus)
	bus.Register(data.HandlerReg{Consumer: "fulfillment.deliver", Type: events.OrderPaid, Fn: delivery.OnOrderPaid, Transactional: true})
	dispatch := func(typ string) {
		ev := d.Client.OutboxEvent.Query().Where(outboxevent.Type(typ)).OnlyX(ctx)
		if err := bus.Dispatch(ctx, events.Envelope{EventID: ev.ID, Type: ev.Type, AggregateID: ev.AggregateID, Payload: ev.Payload}); err != nil {
			t.Fatal(err)
		}
	}
	input := ordermod.CreateOrderInput{UserID: 3, QueryPassword: "test-password", IdempotencyKey: "manual-notify", Items: []ordermod.OrderItemInput{{ProductID: p.ID, Quantity: 1}}}
	quote := input
	quote.QuoteOnly = true
	if _, err := uc.CreateOrder(ctx, quote); err != nil {
		t.Fatal(err)
	}
	if d.Client.OutboxEvent.Query().CountX(ctx) != 0 || d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("quote left an event or order")
	}
	rollback := fmt.Errorf("rollback test")
	if err := data.Tx(ctx, d, func(tx context.Context) error {
		if _, err := uc.CreateOrder(tx, input); err != nil {
			return err
		}
		return rollback
	}); err != rollback {
		t.Fatal(err)
	}
	if d.Client.OutboxEvent.Query().CountX(ctx) != 0 || d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("rollback left an event or order")
	}
	result, err := uc.CreateOrder(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := uc.CreateOrder(ctx, input); err != nil || replay.OrderNo != result.OrderNo {
		t.Fatalf("idempotent retry: %v %v", replay, err)
	}
	created := d.Client.OutboxEvent.Query().Where(outboxevent.Type(events.OrderCreated)).CountX(ctx)
	t.Logf("after CreateOrder: order.created events=%d, deliveries=%d", created, d.Client.OrderDelivery.Query().CountX(ctx))
	if created != 1 {
		t.Fatalf("successful and replayed CreateOrder must publish exactly one order.created event; got %d", created)
	}
	dispatch(events.OrderCreated)
	if err := notifications.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "新订单") || !strings.Contains(sent[0], "不代表付款成功") || strings.Contains(sent[0], "待人工发货") {
		t.Fatalf("unsafe new-order reminder: %v", sent)
	}
	sent = nil
	if err := uc.MarkPaid(ctx, result.OrderNo); err != nil {
		t.Fatal(err)
	}
	dispatch(events.OrderPaid)
	dispatch(events.OrderPaid)
	if d.Client.OrderDelivery.Query().CountX(ctx) != 0 {
		t.Fatal("manual item delivered unexpectedly")
	}
	if len(sent) != 0 {
		t.Fatal("enqueue should not send directly")
	}
	if err := notifications.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "付款成功") || !strings.Contains(sent[0], "人工服务") || !strings.Contains(sent[0], "待人工发货") {
		t.Fatalf("paid reminder failed before manual delivery: %v", sent)
	}
	t.Log("PASS: payment reminder sent before any manual delivery; card delivery count is still zero")
	if err := delivery.ManualDeliver(ctx, result.OrderNo, "TEST-LOCAL-CARD", "", "test", 1); err != nil {
		t.Fatal(err)
	}
	dispatch(events.OrderDelivered)
	if err := notifications.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || !strings.Contains(sent[1], "发货完成") || strings.Contains(sent[1], "待人工发货") {
		t.Fatalf("delivery reminder incorrect: %v", sent)
	}
	if d.Client.OutboxEvent.Query().Where(outboxevent.Type(events.OrderPaid)).CountX(ctx) != 1 {
		t.Fatal("manual delivery re-published payment")
	}
	t.Log("PASS: later manual delivery sends a separate delivery message, without re-publishing order.paid")
}

type heldOrderEmail struct {
	entered chan struct{}
	release chan struct{}
}

func (*heldOrderEmail) Name() string               { return "email" }
func (*heldOrderEmail) Ready(context.Context) bool { return true }
func (c *heldOrderEmail) Deliver(ctx context.Context, _ notifyport.Message) error {
	close(c.entered)
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestTelegramPaidSendsWhileEmailBlocked(t *testing.T) {
	r := newNotifyRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o := r.data.Client.Order.Create().SetOrderNo("EMAIL-BLOCK").SetContact("test@example.test").SetStatus("paid").SetTotalAmount(1000).SaveX(ctx)
	if err := settingsmod.NewRepoImpl(r.data).Put(ctx, "site", "url", []byte(`"https://example.test"`)); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"order_id": o.ID, "order_no": o.OrderNo})
	ev := r.data.Client.OutboxEvent.Create().SetModule("order").SetType(events.OrderPaid).SetAggregateID(o.OrderNo).SetPayload(payload).SetDedupeKey("email-paid").SaveX(ctx)
	email := &heldOrderEmail{entered: make(chan struct{}), release: make(chan struct{})}
	channel := NewTelegramChannel(telegramSettings())
	calls := 0
	channel.client = &http.Client{Transport: telegramTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return telegramResponse(200, `{"ok":true,"result":{"message_id":9}}`), nil
	})}
	d := NewDispatcher(r, email, channel)
	bus := data.NewDispatcher(r.data, nil)
	d.RegisterTelegramOrders(bus)
	bus.Register(data.HandlerReg{Consumer: "notify.dispatcher", Type: events.OrderPaid, Fn: d.HandleEvent})
	done := make(chan error, 1)
	go func() {
		done <- bus.Dispatch(ctx, events.Envelope{EventID: ev.ID, Type: events.OrderPaid, AggregateID: o.OrderNo, Payload: payload})
	}()
	defer func() {
		close(email.release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-email.entered:
	case <-ctx.Done():
		t.Fatal("email handler not reached")
	}
	if n := r.data.Client.NotificationLog.Query().Where(notificationlog.ChannelEQ(notificationlog.ChannelTelegram)).CountX(ctx); n != 2 {
		t.Fatalf("TG not committed before SMTP: %d", n)
	}
	if err := d.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("TG did not send independently while SMTP blocked: %d", calls)
	}
}
