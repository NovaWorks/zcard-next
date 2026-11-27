package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	ticketmod "github.com/NovaWorks/zcard-next/server/internal/mods/ticket"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
)

type ticketNotifySettings struct{ fakeSettings }

func (s ticketNotifySettings) Get(ctx context.Context, g, k string) (json.RawMessage, error) {
	return s.GetJSON(ctx, g, k)
}
func (s ticketNotifySettings) GetDefault(ctx context.Context, g, k string, def json.RawMessage) (json.RawMessage, error) {
	raw, err := s.Get(ctx, g, k)
	if err == nil && len(raw) == 0 {
		return def, nil
	}
	return raw, err
}
func ticketTelegramSettings() ticketNotifySettings {
	s := ticketNotifySettings{telegramSettings()}
	s.values["notify.telegram_ticket_enabled"] = "true"
	s.values["notify.telegram_order_enabled"] = "false"
	s.values["notify.telegram_ticket_events"] = `["ticket.created","ticket.replied","ticket.urgent"]`
	s.values["notify.telegram_targets"] = `[{"chat_id":"-10011","topic_id":7},{"chat_id":"-10011","topic_id":8}]`
	s.values["site.name"] = `"Store <test>"`
	s.values["site.url"] = `"https://store.example"`
	s.values["ticket.urgent_fee"] = "0"
	return s
}

func TestTelegramTicketLifecycle(t *testing.T) {
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser})
	r := newNotifyRepo(t)
	settings := ticketTelegramSettings()
	channel := NewTelegramChannel(settings)
	var sent []map[string]any
	channel.client = &http.Client{Transport: telegramTransport(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sent = append(sent, body)
		return telegramResponse(200, `{"ok":true,"result":{"message_id":9}}`), nil
	})}
	notifications := NewDispatcher(r, channel)
	notifications.adminPath = func(context.Context) (string, error) { return "/private-admin", nil }
	bus := data.NewDispatcher(r.data, slog.New(slog.DiscardHandler))
	notifications.RegisterTelegramTickets(bus)
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	repo := ticketmod.NewTicketRepo(r.data, nil)
	store := ticketmod.NewStoreTicketService(repo, gen, nil, settings, data.NewOutboxWriter(r.data), nil)
	admin := ticketmod.NewAdminTicketService(repo, data.NewOutboxWriter(r.data))
	ticket, err := store.CreateTicket(ctx, &storefrontv1.CreateTicketRequest{Type: "aftersale", Content: "PRIVATE content <script>bad</script>"})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"PRIVATE first followup", "PRIVATE second followup"} {
		if _, err := store.ReplyTicket(ctx, &storefrontv1.ReplyTicketRequest{TicketNo: ticket.TicketNo, Content: body}); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{"PRIVATE staff first", "PRIVATE staff second"} {
		if _, err := admin.ReplyTicket(ctx, &adminv1.AdminReplyRequest{TicketNo: ticket.TicketNo, Content: body}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := admin.ReplyTicket(ctx, &adminv1.AdminReplyRequest{TicketNo: ticket.TicketNo, Content: "PRIVATE INTERNAL", IsInternal: true}); err != nil {
		t.Fatal(err)
	}
	fee := int64(0)
	for range 2 {
		if _, err := store.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: ticket.TicketNo, ExpectedFeeCents: &fee}); err != nil {
			t.Fatal(err)
		}
	}
	rows := r.data.Client.OutboxEvent.Query().Order(ent.Asc("id")).AllX(ctx)
	if len(rows) != 6 {
		t.Fatalf("expected created + four public replies + one urgency, got %d", len(rows))
	}
	for _, row := range rows {
		env := events.Envelope{EventID: row.ID, Type: row.Type, AggregateID: row.AggregateID, Payload: row.Payload}
		for range 2 {
			if err := bus.Dispatch(ctx, env); err != nil {
				t.Fatal(err)
			}
		}
	}
	logs := r.data.Client.NotificationLog.Query().AllX(ctx)
	if len(logs) != 8 {
		t.Fatalf("four merchant alerts times two topics, got %d", len(logs))
	}
	for _, log := range logs {
		if log.BizType != "ticket" || log.BizID != ticket.Id || log.Status != notificationlog.StatusPending {
			t.Fatal(log)
		}
		if strings.Contains(log.Body, "PRIVATE") || !strings.Contains(log.Body, "/private-admin/ticket?ticket_no=") {
			t.Fatal(log.Body)
		}
	}
	if err := notifications.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 8 {
		t.Fatal("lost/duplicate sends", len(sent))
	}
	for _, body := range sent {
		if body["chat_id"] != "-10011" || (body["message_thread_id"] != float64(7) && body["message_thread_id"] != float64(8)) {
			t.Fatal(body)
		}
	}
	if err := notifications.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 8 {
		t.Fatal("delivered notifications replayed")
	}
}

func TestTelegramTicketControlsAndScope(t *testing.T) {
	for _, kind := range []string{"disabled", "unselected", "subsite-envelope", "subsite-payload", "subsite-order", "internal-note", "staff-reply", "wrong-message"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			r := newNotifyRepo(t)
			settings := ticketTelegramSettings()
			ch := NewTelegramChannel(settings)
			d := NewDispatcher(r, ch)
			ticket := r.data.Client.Ticket.Create().SetTicketNo("T-CONTROLS").SetType("aftersale").SaveX(ctx)
			message := r.data.Client.TicketMessage.Create().SetTicketID(ticket.ID).SetSenderType("user").SetContent("PRIVATE").SaveX(ctx)
			payload := map[string]any{"ticket_id": ticket.ID, "ticket_no": ticket.TicketNo, "message_id": message.ID, "by": "user"}
			env := events.Envelope{EventID: 0, Type: events.TicketReplied}
			switch kind {
			case "disabled":
				settings.values["notify.telegram_ticket_enabled"] = "false"
			case "unselected":
				settings.values["notify.telegram_ticket_events"] = `["ticket.created"]`
			case "subsite-envelope":
				env.SubsiteID = 9
			case "subsite-payload":
				payload["subsite_id"] = 9
			case "subsite-order":
				order := r.data.Client.Order.Create().SetOrderNo("O-SUB").SetSubsiteID(9).SaveX(ctx)
				r.data.Client.Ticket.UpdateOneID(ticket.ID).SetOrderID(order.ID).SaveX(ctx)
			case "internal-note":
				r.data.Client.TicketMessage.UpdateOneID(message.ID).SetIsInternal(true).SaveX(ctx)
			case "staff-reply":
				r.data.Client.TicketMessage.UpdateOneID(message.ID).SetSenderType("admin").SaveX(ctx)
			case "wrong-message":
				other := r.data.Client.Ticket.Create().SetTicketNo("T-OTHER").SetType("aftersale").SaveX(ctx)
				r.data.Client.TicketMessage.UpdateOneID(message.ID).SetTicketID(other.ID).SaveX(ctx)
			}
			env.Payload, _ = json.Marshal(payload)
			if err := d.EnqueueTelegramTicket(ctx, env); err != nil {
				t.Fatal(err)
			}
			if n := r.data.Client.NotificationLog.Query().CountX(ctx); n != 0 {
				t.Fatal("unwanted alert", n)
			}
		})
	}
}

func TestTelegramTicketQueuedAlertRespectsLaterDisable(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	settings := ticketTelegramSettings()
	ch := NewTelegramChannel(settings)
	ch.client = &http.Client{Transport: telegramTransport(func(*http.Request) (*http.Response, error) { t.Fatal("disabled ticket alert sent"); return nil, nil })}
	d := NewDispatcher(r, ch)
	if err := r.enqueueTelegram(ctx, 1, events.TicketCreated, 1, "ticket", "metadata", telegramTargets(&TelegramConfig{ChatIDs: "11"}), "ticket"); err != nil {
		t.Fatal(err)
	}
	settings.values["notify.telegram_ticket_enabled"] = "false"
	if err := d.deliverTelegramDue(ctx); err != nil {
		t.Fatal(err)
	}
	log := r.data.Client.NotificationLog.Query().OnlyX(ctx)
	if log.Status != notificationlog.StatusSkipped {
		t.Fatal(log.Status)
	}
}
