package notify

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"html"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

var telegramOrderEvents = map[string]string{
	events.OrderCreated: "新订单", events.OrderPaid: "付款成功", events.OrderDelivered: "发货完成", events.OrderRefunded: "退款成功",
}

func TelegramEvents() []string {
	return []string{events.OrderCreated, events.OrderPaid, events.OrderDelivered, events.OrderRefunded}
}
func (d *Dispatcher) telegram() *TelegramChannel {
	c, _ := d.channels["telegram"].(*TelegramChannel)
	return c
}
func telegramEnabled(cfg *TelegramConfig, event string) bool {
	if cfg == nil || !cfg.Enabled || cfg.BotToken == "" {
		return false
	}
	if event == "telegram.test" {
		return cfg.OrderEnabled || cfg.LowStockEnabled
	}
	if event == "stock.low" {
		return cfg.LowStockEnabled
	}
	if !cfg.OrderEnabled {
		return false
	}
	for _, t := range cfg.Events {
		if t == event {
			return true
		}
	}
	return false
}
func telegramTargets(cfg *TelegramConfig) []notifyport.TelegramTarget {
	if cfg.Targets != nil {
		return cfg.Targets
	}
	out := []notifyport.TelegramTarget{}
	seen := map[notifyport.TelegramTarget]bool{}
	for _, id := range splitComma(cfg.ChatIDs) {
		t, err := notifyport.NormalizeTelegramTarget(notifyport.TelegramTarget{ChatID: id})
		if err == nil && !seen[t] {
			out = append(out, t)
			seen[t] = true
		}
	}
	return out
}

func telegramDeliveryKey(eventID uint64, target notifyport.TelegramTarget) string {
	key := fmt.Sprintf("telegram:%d:%s", eventID, target.ChatID)
	if target.TopicID != 0 {
		key += ":topic:" + strconv.FormatInt(target.TopicID, 10)
	}
	return key
}

// Missing metadata is a legacy delivery to the chat's default destination.
func telegramLogTarget(row *ent.NotificationLog) (notifyport.TelegramTarget, error) {
	t := notifyport.TelegramTarget{ChatID: row.Recipient}
	if raw, ok := row.Variables["telegram_topic_id"]; ok {
		v, ok := raw.(string)
		if !ok {
			return t, fmt.Errorf("通知话题记录无效，已停止发送")
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > 2147483647 {
			return t, fmt.Errorf("通知话题记录无效，已停止发送")
		}
		t.TopicID = n
	}
	return notifyport.NormalizeTelegramTarget(t)
}

func telegramTargetEnabled(cfg *TelegramConfig, event string, target notifyport.TelegramTarget) bool {
	if !telegramEnabled(cfg, event) {
		return false
	}
	for _, t := range telegramTargets(cfg) {
		if t == target {
			return true
		}
	}
	return false
}

// EnqueueTelegram is a DB-only transactional outbox subscriber. It never calls Telegram.
// This first version is for the main merchant. Subsite events cannot leak to main-site chats.
func (d *Dispatcher) EnqueueTelegram(ctx context.Context, env events.Envelope) error {
	c := d.telegram()
	if c == nil || env.SubsiteID != 0 {
		return nil
	}
	if _, ok := telegramOrderEvents[env.Type]; !ok {
		return nil
	}
	cfg, err := c.tgConfig(ctx)
	if err != nil {
		return err
	}
	if !telegramEnabled(cfg, env.Type) {
		return nil
	}
	targets := telegramTargets(cfg)
	if len(targets) == 0 {
		return nil
	}
	client := data.Client(ctx, d.repo.data)
	var payload struct {
		OrderNo string `json:"order_no"`
		OrderID uint64 `json:"order_id"`
	}
	if err = json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	q := client.Order.Query().Where(order.SubsiteID(0))
	if payload.OrderID > 0 {
		q = q.Where(order.ID(payload.OrderID))
	} else {
		no := payload.OrderNo
		if no == "" {
			no = env.AggregateID
		}
		q = q.Where(order.OrderNo(no))
	}
	o, err := q.Only(ctx)
	if err != nil {
		return err
	}
	items, err := client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).Order(ent.Asc(orderitem.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	if env.EventID > 0 {
		event, err := client.OutboxEvent.Get(ctx, env.EventID)
		if err != nil {
			return err
		}
		at = event.CreatedAt
	}
	amount := o.TotalAmount
	amountLabel := "订单金额"
	payChannel := o.PaymentChannel
	if env.Type != events.OrderCreated {
		p, err := client.Payment.Query().Where(payment.OrderID(o.ID), payment.StatusEQ(payment.StatusSuccess)).Order(ent.Desc(payment.FieldID)).First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if p != nil {
			amount = p.Amount
			payChannel = p.Channel
			amountLabel = "支付金额"
		}
	}
	read := func(key string) (string, error) {
		raw, e := c.settings.GetJSON(ctx, "site", key)
		var v string
		if e == nil && len(raw) > 0 {
			e = json.Unmarshal(raw, &v)
		}
		return v, e
	}
	name, err := read("name")
	if err != nil {
		return err
	}
	if name == "" {
		name = "ZCard"
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
	if len([]rune(name)) > 30 {
		name = string([]rune(name)[:30])
	}
	subject := html.EscapeString(name + " · " + telegramOrderEvents[env.Type])
	lines := []string{"订单号：" + html.EscapeString(o.OrderNo), fmt.Sprintf("%s：%s %d.%02d", amountLabel, html.EscapeString(o.BaseCurrency), amount/100, amount%100), "支付渠道：" + html.EscapeString(payChannel), "事件时间：" + at.Format(time.RFC3339)}
	for i, it := range items {
		if i >= 10 {
			lines = append(lines, "更多商品请查看订单详情")
			break
		}
		label := it.ProductName
		if it.SkuName != "" {
			label += " / " + it.SkuName
		}
		if len([]rune(label)) > 100 {
			label = string([]rune(label)[:100]) + "…"
		}
		kind := string(it.FulfillmentType)
		lines = append(lines, fmt.Sprintf("• %s × %d（%s）", html.EscapeString(label), it.Quantity, html.EscapeString(map[string]string{"auto": "自动发货", "manual": "人工服务", "upstream": "上游交付"}[kind])))
		if kind == "manual" || kind == "manual_service" {
			lines = append(lines, "人工服务：请在后台查看并处理")
		}
	}
	if u, e := url.Parse(base); e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
		lines = append(lines, `<a href="`+html.EscapeString(strings.TrimRight(base, "/")+path+"/order?order_no="+url.QueryEscape(o.OrderNo))+`">查看后台订单（需要登录）</a>`)
	}
	// Escape after limiting individual fields; never split HTML entities/tags.
	body := strings.Join(lines, "\n")
	return d.repo.enqueueTelegram(ctx, env.EventID, env.Type, o.ID, subject, body, targets)
}
func (r *NotifyRepo) enqueueTelegram(ctx context.Context, eventID uint64, event string, bizID uint64, subject, body string, targets []notifyport.TelegramTarget) error {
	for _, target := range targets {
		key := telegramDeliveryKey(eventID, target)
		vars := map[string]any{}
		if target.TopicID != 0 {
			vars["telegram_topic_id"] = strconv.FormatInt(target.TopicID, 10)
		}
		err := data.Client(ctx, r.data).NotificationLog.Create().SetVariables(vars).SetDeliveryKey(key).SetEventType(event).SetBizType("order").SetBizID(bizID).
			SetChannel(notificationlog.ChannelTelegram).SetRecipient(target.ChatID).SetSubject(subject).SetBody(body).
			SetNextAttemptAt(time.Now().UTC()).OnConflict(sql.ConflictColumns(notificationlog.FieldDeliveryKey)).DoNothing().Exec(ctx)
		if err != nil && !errors.Is(err, dbsql.ErrNoRows) {
			return err
		}
	}
	return nil
}

// ScanTelegram uses a persisted lease so overlapping scans/restarts do not resend successes.
func (d *Dispatcher) ScanTelegram(ctx context.Context) {
	if err := d.deliverTelegramDue(ctx); err != nil {
		slog.ErrorContext(ctx, "notify.telegram.scan", "error", err)
	}
}
func (d *Dispatcher) deliverTelegramDue(ctx context.Context) error {
	c := d.telegram()
	if c == nil {
		return nil
	}
	now := time.Now().UTC()
	client := data.Client(ctx, d.repo.data)
	due := notificationlog.And(notificationlog.ChannelEQ(notificationlog.ChannelTelegram), notificationlog.DeliveryKeyNotNil(), notificationlog.StatusEQ(notificationlog.StatusPending), notificationlog.NextAttemptAtLTE(now), notificationlog.Or(notificationlog.LeaseUntilIsNil(), notificationlog.LeaseUntilLTE(now)))
	rows, err := client.NotificationLog.Query().Where(due).Order(ent.Asc(notificationlog.FieldID)).Limit(20).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lease := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
		n, err := client.NotificationLog.Update().Where(notificationlog.ID(row.ID), due).SetLeaseUntil(lease).AddAttempts(1).Save(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		cfg, configErr := c.tgConfig(ctx)
		if configErr == nil && row.EventType == "stock.low" {
			raw, e := c.settings.GetJSON(ctx, "supply", "low_stock_alert_enabled")
			if e != nil {
				configErr = e
			} else if len(raw) > 0 {
				var enabled bool
				if e = json.Unmarshal(raw, &enabled); e != nil {
					configErr = e
				} else if !enabled && cfg != nil {
					cfg.LowStockEnabled = false
				}
			}
			if threshold, ok := row.Variables["stock_threshold"].(string); ok && cfg != nil {
				raw, e = c.settings.GetJSON(ctx, "supply", "low_stock_threshold")
				if e != nil {
					configErr = e
				} else {
					current := 5
					if len(raw) > 0 {
						e = json.Unmarshal(raw, &current)
					}
					if e != nil {
						configErr = e
					} else if strconv.Itoa(current) != threshold {
						cfg.LowStockEnabled = false
					}
				}
			}
		}
		var sendErr error
		var messageID string
		target, targetErr := telegramLogTarget(row)
		status := notificationlog.StatusSent
		if configErr != nil {
			sendErr = configErr
		} else if targetErr != nil {
			sendErr = &TelegramError{Permanent: true, Detail: targetErr.Error()}
		} else if !telegramTargetEnabled(cfg, row.EventType, target) {
			status = notificationlog.StatusSkipped
		} else {
			messageID, sendErr = c.sendResult(ctx, cfg.BotToken, target.ChatID, row.Subject+"\n"+row.Body, target.TopicID)
		}
		update := client.NotificationLog.Update().Where(notificationlog.ID(row.ID), notificationlog.LeaseUntilEQ(lease), notificationlog.StatusEQ(notificationlog.StatusPending)).ClearLeaseUntil().ClearNextAttemptAt().SetMessageID(messageID).SetErrorMessage("")
		if status == notificationlog.StatusSkipped {
			update.SetErrorMessage("通知已关闭或原接收位置已移除，未改投其他话题")
		}
		if sendErr != nil {
			status = notificationlog.StatusFailed
			delay := time.Duration(1<<min(row.Attempts, 8)) * 30 * time.Second
			var te *TelegramError
			permanent := errors.As(sendErr, &te) && te.Permanent
			if te != nil && te.RetryAfter > delay {
				delay = te.RetryAfter
			}
			if !permanent && row.Attempts+1 < 6 {
				status = notificationlog.StatusPending
				update.SetNextAttemptAt(time.Now().UTC().Add(delay))
			}
			update.SetErrorMessage(sendErr.Error())
		}
		if _, err = update.SetStatus(status).Save(ctx); err != nil {
			return err
		}
	}
	return nil
}
