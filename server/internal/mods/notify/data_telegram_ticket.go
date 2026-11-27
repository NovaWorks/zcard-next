package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticketmessage"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

func TelegramTicketEvents() []string {
	return []string{events.TicketCreated, events.TicketReplied, events.TicketUrgent}
}

func (d *Dispatcher) RegisterTelegramTickets(bus *data.Dispatcher) {
	for _, event := range TelegramTicketEvents() {
		bus.Register(data.HandlerReg{Consumer: "notify.telegram_tickets", Type: event, Fn: d.EnqueueTelegramTicket, Transactional: true})
	}
}

// EnqueueTelegramTicket shares the durable sender and destinations with orders.
// It sends metadata only; message bodies, attachments and guest contacts stay in
// the authenticated workbench. Internal notes and staff replies are not alerts.
func (d *Dispatcher) EnqueueTelegramTicket(ctx context.Context, env events.Envelope) error {
	c := d.telegram()
	if c == nil || env.SubsiteID != 0 {
		return nil
	}
	labels := map[string]string{events.TicketCreated: "新工单", events.TicketReplied: "客户追加回复", events.TicketUrgent: "工单加急"}
	label, supported := labels[env.Type]
	if !supported {
		return nil
	}
	cfg, err := c.tgConfig(ctx)
	if err != nil {
		return err
	}
	if !telegramEnabled(cfg, env.Type) || len(telegramTargets(cfg)) == 0 {
		return nil
	}
	var payload struct {
		TicketID  uint64 `json:"ticket_id"`
		TicketNo  string `json:"ticket_no"`
		MessageID uint64 `json:"message_id"`
		SubsiteID uint64 `json:"subsite_id"`
		By        string `json:"by"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	if payload.SubsiteID != 0 || (env.Type == events.TicketReplied && payload.By != "user") {
		return nil
	}
	client := data.Client(ctx, d.repo.data)
	t, err := client.Ticket.Get(ctx, payload.TicketID)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.TicketNo != payload.TicketNo {
		return fmt.Errorf("工单通知事件与工单不一致")
	}
	// Tickets predate a dedicated tenant column. Verify related stored order/product scope
	// as well as the server-issued event scope before sending to main-site chats.
	orderNo := ""
	if t.OrderID > 0 {
		order, err := client.Order.Get(ctx, t.OrderID)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if order.SubsiteID != 0 {
			return nil
		}
		orderNo = order.OrderNo
	}
	if t.ProductID > 0 {
		product, err := client.Product.Get(ctx, t.ProductID)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if product.SubsiteID != 0 {
			return nil
		}
	}
	if env.Type == events.TicketReplied {
		message, err := client.TicketMessage.Get(ctx, payload.MessageID)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if message.TicketID != t.ID || message.IsInternal || message.SenderType != ticketmessage.SenderTypeUser {
			return nil
		}
	}
	read := func(key string) (string, error) {
		raw, err := c.settings.GetJSON(ctx, "site", key)
		if err != nil {
			return "", err
		}
		var value string
		if len(raw) > 0 {
			err = json.Unmarshal(raw, &value)
		}
		return value, err
	}
	name, err := read("name")
	if err != nil {
		return err
	}
	if name == "" {
		name = "ZCard"
	}
	if len([]rune(name)) > 30 {
		name = string([]rune(name)[:30])
	}
	at := time.Now().UTC()
	if env.EventID > 0 {
		event, err := client.OutboxEvent.Get(ctx, env.EventID)
		if err != nil {
			return err
		}
		at = event.CreatedAt
	}
	kind := map[string]string{"presale": "售前", "aftersale": "售后"}[string(t.Type)]
	status := map[string]string{"open": "待处理", "processing": "处理中", "resolved": "已解决", "closed": "已关闭"}[string(t.Status)]
	priority := map[string]string{"normal": "普通", "urgent_paid": "加急"}[string(t.Priority)]
	lines := []string{"工单号：" + html.EscapeString(t.TicketNo), "类型：" + kind, "当前状态：" + status, "优先级：" + priority, "事件时间：" + at.Format(time.RFC3339)}
	if orderNo != "" {
		lines = append(lines, "关联订单："+html.EscapeString(orderNo))
	}
	base, err := read("url")
	if err != nil {
		return err
	}
	path := "/admin"
	if d.adminPath != nil {
		path, err = d.adminPath(ctx)
		if err != nil {
			return err
		}
	}
	if u, err := url.Parse(base); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil {
		u.Path = strings.TrimRight(u.Path, "/") + path + "/ticket"
		u.RawPath = ""
		u.RawQuery = url.Values{"ticket_no": {t.TicketNo}}.Encode()
		u.Fragment = ""
		lines = append(lines, `<a href="`+html.EscapeString(u.String())+`">查看后台工单（需要登录）</a>`)
	}
	return d.repo.enqueueTelegram(ctx, env.EventID, env.Type, t.ID, html.EscapeString(name+" · "+label), strings.Join(lines, "\n"), telegramTargets(cfg), "ticket")
}
