package notify

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/stockalert"
	"github.com/google/uuid"
)

func (d *Dispatcher) ScanLowStock(ctx context.Context) {
	if err := d.scanLowStock(ctx); err != nil {
		slog.WarnContext(ctx, "notify.low_stock.scan", "error", err)
	}
}
func (d *Dispatcher) scanLowStock(ctx context.Context) error {
	channel := d.telegram()
	if channel == nil {
		return nil
	}
	cfg, err := channel.tgConfig(ctx)
	if err != nil {
		return err
	}
	if !telegramEnabled(cfg, "stock.low") || len(telegramTargets(cfg)) == 0 {
		return nil
	}
	threshold := 5
	raw, err := channel.settings.GetJSON(ctx, "supply", "low_stock_threshold")
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &threshold); err != nil {
			return err
		}
	}
	if threshold < 1 || threshold > 1000000 {
		return fmt.Errorf("低库存提醒阈值无效")
	}
	raw, err = channel.settings.GetJSON(ctx, "supply", "low_stock_alert_enabled")
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		var enabled bool
		if err = json.Unmarshal(raw, &enabled); err != nil {
			return err
		}
		if !enabled {
			return nil
		}
	}
	baseRaw, err := channel.settings.GetJSON(ctx, "site", "url")
	if err != nil {
		return err
	}
	var base string
	_ = json.Unmarshal(baseRaw, &base)
	path := "/admin"
	if d.adminPath != nil {
		path, err = d.adminPath(ctx)
		if err != nil {
			return err
		}
	}
	link := ""
	if u, e := url.Parse(base); e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
		link = "\n<a href=\"" + html.EscapeString(strings.TrimRight(base, "/")+path+"/product?low_stock=1") + "\">查看后台库存预警（需要登录）</a>"
	}
	var last uint64
	for {
		rows, err := data.Client(ctx, d.repo.data).Product.Query().Where(product.IDGT(last), product.SubsiteID(0), product.IsLocked(false), product.Or(product.StatusGT(0), product.And(product.Status(0), product.AutoListing(true), product.ListingReason("stock_out")))).Order(ent.Asc(product.FieldID)).Limit(100).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		last = rows[len(rows)-1].ID
		changed := 0
		err = data.Tx(ctx, d.repo.data, func(tx context.Context) error {
			c := data.Client(tx, d.repo.data)
			lines := []string{}
			for _, old := range rows {
				p, e := data.GuardProductWrite(tx, d.repo.data, old.ID)
				if data.IsProductLocked(e) || ent.IsNotFound(e) {
					continue
				}
				if e != nil {
					return e
				}
				if p.Status < 0 || p.SubsiteID != 0 || p.Status == 0 && (!p.AutoListing || p.ListingReason != "stock_out") {
					continue
				}
				slots, e := data.StockSlots(tx, d.repo.data, p)
				if e != nil {
					return e
				}
				for _, slot := range slots {
					if slot.Quantity < -1 {
						continue
					} // Unknown cannot recover or trigger an episode.
					e = c.StockAlert.Create().SetProductID(p.ID).SetSkuID(slot.SKUID).SetSourceKey(slot.SourceKey).OnConflict(entsql.ConflictColumns(stockalert.FieldProductID, stockalert.FieldSkuID)).DoNothing().Exec(tx)
					if e != nil && !errors.Is(e, dbsql.ErrNoRows) {
						return e
					}
					row, e := c.StockAlert.Query().Where(stockalert.ProductID(p.ID), stockalert.SkuID(slot.SKUID)).Only(tx)
					if e != nil {
						return e
					}
					state := int8(0)
					if slot.Quantity >= 0 && slot.Quantity < int64(threshold) {
						state = 1
						if slot.Quantity == 0 {
							state = 2
						}
					}
					notified := row.NotifiedState
					if state == 0 || row.SourceKey != slot.SourceKey || row.Threshold != threshold {
						notified = 0
					}
					now := time.Now().UTC()
					// Only a worsening state sends, so zero -> low does not create chatter.
					send := state > notified && (row.LastNotifiedAt == 0 || now.Unix()-row.LastNotifiedAt >= 1800 || state == 2 && notified == 1)
					upd := c.StockAlert.UpdateOneID(row.ID).SetSourceKey(slot.SourceKey).SetThreshold(threshold).SetState(state).SetNotifiedState(notified)
					if send {
						upd.SetNotifiedState(state).SetLastNotifiedAt(now.Unix())
						changed++
						if len(lines) < 10 {
							label := p.Name
							if slot.Name != "" {
								label += " / " + slot.Name
							}
							label = limitStockText(label, 70)
							kind := "低库存"
							if state == 2 {
								kind = "已售罄"
							}
							lines = append(lines, fmt.Sprintf("• %s【%s】\n库存：%d；阈值：低于 %d\n检查时间：%s", html.EscapeString(label), kind, slot.Quantity, threshold, slot.CheckedAt.UTC().Format(time.RFC3339)))
						}
					}
					if e = upd.Exec(tx); e != nil {
						return e
					}
				}
			}
			if changed == 0 {
				return nil
			}
			if changed > len(lines) {
				lines = append(lines, fmt.Sprintf("另有 %d 个低库存规格，请在后台查看。", changed-len(lines)))
			}
			key := "stock:" + uuid.NewString()
			for _, target := range telegramTargets(cfg) {
				vars := map[string]any{"stock_threshold": fmt.Sprint(threshold)}
				if target.TopicID > 0 {
					vars["telegram_topic_id"] = fmt.Sprint(target.TopicID)
				}
				err = c.NotificationLog.Create().SetVariables(vars).SetDeliveryKey(fmt.Sprintf("%s:%s:%d", key, target.ChatID, target.TopicID)).SetEventType("stock.low").SetBizType("stock").SetChannel(notificationlog.ChannelTelegram).SetRecipient(target.ChatID).SetSubject("库存预警").SetBody(strings.Join(lines, "\n\n") + link).SetNextAttemptAt(time.Now().UTC()).Exec(tx)
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		// At most one digest per minute. Remaining products are picked on later scans
		// because already-notified episodes persist, including across process restarts.
		if changed > 0 {
			return nil
		}
	}
}
func limitStockText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
