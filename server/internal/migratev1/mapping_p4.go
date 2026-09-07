package migratev1

// P4 交易域迁移：orders 五表合成（orders/order_items/order_amount_lines/
// order_status_events）→ payments（聚合支付取首单+raw 保真）→ recharge_orders →
// coupons → order_deliveries → cards.order_id 回填。
// 映射规格《数据迁移工具开发计划》§5.5 与附录 A；对外单号 order_no 原样保留。
//
// 金额不变量：total_amount = SUM(order_amount_lines.amount)（base_price 正、
// coupon_discount 负、subsite_markup 正，按需合成）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
)

// MigrateTrade P4 阶段。
func (m *Migrator) MigrateTrade(ctx context.Context) error {
	steps := []func(context.Context) error{
		m.migrateOrders,
		m.migrateRecharges, // 先于 payments（payments.recharge_order_id 引用）
		m.migratePayments,
		m.migrateCoupons,
		m.migrateOrderDeliveries,
		m.backfillCardOrderIDs, // 最后：订单落库后回填 P3 置 0 的 cards.order_id
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// mapOrderStatus 1.x (status, delivery_status) → 2.0 状态（附录 A）。
func mapOrderStatus(status, deliveryStatus string) string {
	switch status {
	case "paid":
		if deliveryStatus == "delivered" {
			return "completed"
		}
		return "paid"
	case "closed":
		return "canceled"
	case "refunded":
		return "refunded"
	default: // pending
		return "pending_payment"
	}
}

// migrateOrders 1.x orders（+order_items）→ 2.0 orders + items + amount_lines + status_events。
func (m *Migrator) migrateOrders(ctx context.Context) error {
	var (
		id                                     int64
		userID, quantity                       sql.NullInt64
		merchant, subsiteID                    sql.NullInt64
		productID                              sql.NullInt64
		orderNo                                string
		amount, discount, cost, subsiteProfit  int64
		couponCode                             sql.NullString
		baseCur, dispCur                       sql.NullString
		exchangeRate                           sql.NullFloat64
		amountDisplay                          sql.NullInt64
		status, deliveryStatus                 string
		fulfillmentSnap                        sql.NullString
		payChannel                             sql.NullString
		subsiteDomain                          sql.NullString
		source                                 sql.NullString
		upstreamOrderID                        sql.NullString
		skuName                                sql.NullString
		instructions, deliveryMsg              sql.NullString
		extra                                  sql.NullString
		contact                                sql.NullString
		createDevice                           sql.NullString
		createIP                               sql.NullString
		paidAt, closedAt, createdAt, updatedAt sql.NullString
	)
	return m.scanTable(ctx, "orders",
		[]string{"id", "order_no", "merchant_id", "user_id", "product_id", "quantity",
			"amount", "discount_amount", "cost", "coupon_code", "base_currency", "display_currency",
			"exchange_rate", "amount_display", "status", "delivery_status",
			"fulfillment_type_snapshot", "payment_channel", "subsite_id", "subsite_domain",
			"subsite_profit", "source", "upstream_order_id", "sku_name",
			"instructions_snapshot", "delivery_message_snapshot", "extra", "contact",
			"create_device", "create_ip", "paid_at", "closed_at", "created_at", "updated_at"},
		func() []any {
			return []any{&id, &orderNo, &merchant, &userID, &productID, &quantity,
				&amount, &discount, &cost, &couponCode, &baseCur, &dispCur,
				&exchangeRate, &amountDisplay, &status, &deliveryStatus,
				&fulfillmentSnap, &payChannel, &subsiteID, &subsiteDomain,
				&subsiteProfit, &source, &upstreamOrderID, &skuName,
				&instructions, &deliveryMsg, &extra, &contact,
				&createDevice, &createIP, &paidAt, &closedAt, &createdAt, &updatedAt}
		},
		func(int64) error {
			siteID, siteOK := m.subsiteFor(ctx, nullInt(merchant))
			if !siteOK {
				m.st.Record("orders", "skip") // 分站订单：P6 补
				return nil
			}
			if _, ok := m.IDs.Get(ctx, "orders", uint64(id)); ok {
				m.st.Record("orders", "skip")
				return nil
			}
			ca, caOK, err := mustTime(nullStr(createdAt), m.TZ)
			if err != nil {
				return err
			}
			ua, _, err := mustTime(nullStr(updatedAt), m.TZ)
			if err != nil {
				return err
			}
			st := mapOrderStatus(status, deliveryStatus)

			// extra：v1 快照保真 + query_password 提升为列
			extraOut := map[string]any{}
			if e := nullStr(extra); e != "" && json.Valid([]byte(e)) {
				_ = json.Unmarshal([]byte(e), &extraOut)
			}
			queryHash := ""
			if qp, ok := extraOut["query_password"].(string); ok {
				queryHash = qp
				delete(extraOut, "query_password")
			}
			for k, v := range map[string]string{
				"v1_fulfillment_type":  nullStr(fulfillmentSnap),
				"v1_source":            nullStr(source),
				"v1_upstream_order_id": nullStr(upstreamOrderID),
				"v1_instructions":      nullStr(instructions),
				"v1_delivery_message":  nullStr(deliveryMsg),
				"v1_create_device":     nullStr(createDevice),
				"v1_coupon_code":       nullStr(couponCode),
			} {
				if v != "" {
					extraOut[k] = v
				}
			}

			b := m.Client.Order.Create().
				SetOrderNo(orderNo).
				SetSubsiteID(siteID).
				SetTotalAmount(amount).
				SetCost(cost).
				SetSubsiteProfit(subsiteProfit).
				SetStatus(order.Status(st))
			if caOK {
				b.SetCreatedAt(ca)
			}
			b.SetUpdatedAt(ua)
			if u := nullInt(userID); u > 0 {
				if newU, ok := m.IDs.Get(ctx, "users", uint64(u)); ok {
					b.SetUserID(newU)
				}
			}
			if c := nullStr(contact); c != "" {
				if nullInt(userID) > 0 {
					b.SetContact(c)
				} else {
					b.SetGuestContact(c)
				}
			}
			if queryHash != "" {
				b.SetQueryPasswordHash(queryHash) // bcrypt 直迁（$2y$ 兼容已由 golden vector 钉死）
			}
			if bc := nullStr(baseCur); bc != "" {
				b.SetBaseCurrency(bc)
			}
			if dc := nullStr(dispCur); dc != "" {
				b.SetDisplayCurrency(dc)
			}
			if er := nullFloat(exchangeRate); er != 0 {
				b.SetExchangeRate(er)
			}
			if ad := nullInt(amountDisplay); ad != 0 {
				b.SetAmountDisplay(ad)
			}
			if pc := nullStr(payChannel); pc != "" {
				b.SetPaymentChannel(pc)
			}
			if sd := nullStr(subsiteDomain); sd != "" {
				b.SetSubsiteDomain(sd)
			}
			if ip := nullStr(createIP); ip != "" {
				b.SetClientIP(ip)
			}
			if len(extraOut) > 0 {
				b.SetExtra(extraOut)
			}
			if t, ok, err := mustTime(nullStr(paidAt), m.TZ); err != nil {
				return err
			} else if ok {
				b.SetPaidAt(t)
			}
			if t, ok, err := mustTime(nullStr(closedAt), m.TZ); err != nil {
				return err
			} else if ok {
				b.SetClosedAt(t)
				b.SetExpiredAt(t) // 1.x 无独立 expired 概念，closed 即终态锚点
			}
			o, err := b.Save(ctx)
			if err != nil {
				return err
			}

			// order_items：以 1.x order_items 为准（单商品模型恒 1 行）
			qty := nullInt(quantity)
			if qty <= 0 {
				qty = 1
			}
			newPID, pidOK := m.IDs.Get(ctx, "products", uint64(nullInt(productID)))
			fulfillment := mapFulfillmentType(nullStr(fulfillmentSnap))
			fulfillStatus := "pending"
			if deliveryStatus == "delivered" {
				fulfillStatus = "delivered"
			}
			item, err := m.Client.OrderItem.Create().
				SetOrderID(o.ID).
				SetProductID(pidOrZero(newPID, pidOK)).
				SetSkuName(nullStr(skuName)).
				SetUnitPrice(amount / qty).
				SetQuantity(int32(qty)).
				SetAmount(amount).
				SetCost(cost).
				SetFulfillmentType(orderitem.FulfillmentType(fulfillment)).
				SetFulfillmentStatus(fulfillStatus).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("order_items 合成失败: %w", err)
			}
			if !pidOK {
				m.RW.AddError("orders", uint64(id), "商品未迁移（分站？），item.product_id=0")
			}

			// amount_lines（恒等式 total = SUM(lines)）：
			// base_price = amount + discount - subsiteMarkup；coupon_discount 为负；
			// subsite_markup 仅分站单合成（1.x subsite_profit 是利润：分站售价=基础价+利润，
			// 拆 base=amount-profit / markup=+profit；主站单利润只进 orders.subsite_profit 列）
			subsiteMarkup := int64(0)
			if (siteID > 0 || nullInt(subsiteID) > 0) && subsiteProfit > 0 {
				subsiteMarkup = subsiteProfit
			} else if subsiteProfit > 0 {
				m.RW.AddError("orders", uint64(id),
					fmt.Sprintf("主站订单带 subsite_profit=%d（仅记入 orders 列，不参与金额行）", subsiteProfit))
			}
			lines := []*ent.OrderAmountLineCreate{
				m.Client.OrderAmountLine.Create().
					SetOrderID(o.ID).
					SetItemID(item.ID).
					SetType(orderamountline.TypeBasePrice).
					SetAmount(amount + discount - subsiteMarkup).
					SetSeq(1),
			}
			if discount > 0 {
				lines = append(lines, m.Client.OrderAmountLine.Create().
					SetOrderID(o.ID).
					SetItemID(item.ID).
					SetType(orderamountline.TypeCouponDiscount).
					SetAmount(-discount).
					SetSeq(2).
					SetMeta(map[string]any{"v1_coupon_code": nullStr(couponCode)}))
			}
			if subsiteMarkup > 0 {
				lines = append(lines, m.Client.OrderAmountLine.Create().
					SetOrderID(o.ID).
					SetType(orderamountline.TypeSubsiteMarkup).
					SetAmount(subsiteMarkup).
					SetSeq(3))
			}
			if _, err := m.Client.OrderAmountLine.CreateBulk(lines...).Save(ctx); err != nil {
				return fmt.Errorf("amount_lines 合成失败: %w", err)
			}

			// status_events：created/paid/closed 三个锚点（operator=system）
			events := []*ent.OrderStatusEventCreate{
				m.Client.OrderStatusEvent.Create().
					SetOrderID(o.ID).
					SetFromStatus("").
					SetToStatus("pending_payment").
					SetEvent("created").
					SetOperator(orderstatusevent.OperatorSystem).
					SetCreatedAt(ca),
			}
			if t, ok, _ := mustTime(nullStr(paidAt), m.TZ); ok {
				events = append(events, m.Client.OrderStatusEvent.Create().
					SetOrderID(o.ID).
					SetFromStatus("pending_payment").
					SetToStatus("paid").
					SetEvent("paid").
					SetOperator(orderstatusevent.OperatorSystem).
					SetCreatedAt(t))
			}
			if t, ok, _ := mustTime(nullStr(closedAt), m.TZ); ok {
				events = append(events, m.Client.OrderStatusEvent.Create().
					SetOrderID(o.ID).
					SetFromStatus("pending_payment").
					SetToStatus(st).
					SetEvent("canceled").
					SetOperator(orderstatusevent.OperatorSystem).
					SetCreatedAt(t))
			}
			if _, err := m.Client.OrderStatusEvent.CreateBulk(events...).Save(ctx); err != nil {
				return fmt.Errorf("status_events 回填失败: %w", err)
			}

			if _, err := m.IDs.Put(ctx, m.Client, "orders", uint64(id), o.ID); err != nil {
				return err
			}
			m.st.Record("orders", "migrated")
			return nil
		},
	)
}

func mapFulfillmentType(v1 string) string {
	switch v1 {
	case "manual":
		return "manual"
	case "upstream":
		return "upstream"
	default: // auto_card / fixed
		return "auto"
	}
}

func pidOrZero(id uint64, ok bool) uint64 {
	if !ok {
		return 0
	}
	return id
}

// migratePayments 1.x payments → payments。聚合支付（order_ids JSON 数组）：
// order_id 取首单，完整数组保 raw（已确认决策）。
func (m *Migrator) migratePayments(ctx context.Context) error {
	var (
		id                           int64
		rechargeID                   sql.NullInt64
		orderID                      sql.NullInt64
		orderIDs                     sql.NullString
		channel, channelOrderNo      string
		channelOrderNoNull           sql.NullString
		amount, fee                  int64
		status                       string
		chargedCur                   sql.NullString
		chargedAmount                sql.NullInt64
		channelRate                  sql.NullFloat64
		raw                          sql.NullString
		paidAt, createdAt, updatedAt sql.NullString
	)
	_ = channelOrderNo
	return m.scanTable(ctx, "payments",
		[]string{"id", "order_id", "order_ids", "recharge_id", "channel", "channel_order_no",
			"amount", "fee", "status", "charged_currency", "charged_amount",
			"channel_exchange_rate", "raw", "paid_at", "created_at", "updated_at"},
		func() []any {
			return []any{&id, &orderID, &orderIDs, &rechargeID, &channel, &channelOrderNoNull,
				&amount, &fee, &status, &chargedCur, &chargedAmount,
				&channelRate, &raw, &paidAt, &createdAt, &updatedAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "payments", uint64(id)); ok {
				m.st.Record("payments", "skip")
				return nil
			}
			b := m.Client.Payment.Create().
				SetChannel(channel).
				SetAmount(amount).
				SetFee(fee).
				SetStatus(payment.Status(status))
			if no := nullStr(channelOrderNoNull); no != "" {
				b.SetChannelOrderNo(no)
			}
			// 聚合支付：首单关联 + raw 保真
			if oid := nullInt(orderID); oid > 0 {
				if newO, ok := m.IDs.Get(ctx, "orders", uint64(oid)); ok {
					b.SetOrderID(newO)
				}
			} else if idsJSON := nullStr(orderIDs); idsJSON != "" && json.Valid([]byte(idsJSON)) {
				var ids []int64
				if json.Unmarshal([]byte(idsJSON), &ids) == nil && len(ids) > 0 {
					if newO, ok := m.IDs.Get(ctx, "orders", uint64(ids[0])); ok {
						b.SetOrderID(newO)
						m.RW.AddError("payments", uint64(id),
							fmt.Sprintf("聚合支付关联 %d 单，已取首单（完整清单见 raw）", len(ids)))
					}
				}
			}
			if rid := nullInt(rechargeID); rid > 0 {
				if newR, ok := m.IDs.Get(ctx, "recharges", uint64(rid)); ok {
					b.SetRechargeOrderID(newR)
				}
			}
			if cc := nullStr(chargedCur); cc != "" {
				b.SetChargedCurrency(cc)
			}
			// 1.x charged_amount 是实收币种最小单位 → 2.0 charged_units；
			// charged_amount（基础货币实收分）取应收近似，差异由对账报告暴露
			if ca := nullInt(chargedAmount); ca != 0 {
				b.SetChargedUnits(ca).
					SetChargedAmount(amount)
			}
			if er := nullFloat(channelRate); er != 0 {
				b.SetExchangeRate(er)
			}
			if r := nullStr(raw); r != "" && json.Valid([]byte(r)) {
				b.SetRaw(json.RawMessage(r))
			}
			if t, ok, err := mustTime(nullStr(paidAt), m.TZ); err != nil {
				return err
			} else if ok {
				b.SetPaidAt(t)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			p, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "payments", uint64(id), p.ID); err != nil {
				return err
			}
			m.st.Record("payments", "migrated")
			return nil
		},
	)
}

// migrateRecharges 1.x recharges → recharge_orders（1.x 单号无 2.0 列，进报告）。
func (m *Migrator) migrateRecharges(ctx context.Context) error {
	var (
		id, userID, amount int64
		rechargeNo         string
		status, target     string
		paidAt, createdAt  sql.NullString
	)
	return m.scanTable(ctx, "recharges",
		[]string{"id", "recharge_no", "user_id", "amount", "status", "target", "paid_at", "created_at"},
		func() []any {
			return []any{&id, &rechargeNo, &userID, &amount, &status, &target, &paidAt, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "recharges", uint64(id)); ok {
				m.st.Record("recharges", "skip")
				return nil
			}
			newU, ok := m.IDs.Get(ctx, "users", uint64(userID))
			if !ok {
				m.st.Record("recharges", "skip")
				return nil
			}
			st := "pending"
			switch status {
			case "paid":
				st = "success"
			case "closed":
				st = "expired"
			}
			b := m.Client.RechargeOrder.Create().
				SetUserID(newU).
				SetAmount(amount).
				SetTarget(rechargeorder.Target(target))
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			b.SetStatus(rechargeorder.Status(st))
			if t, ok, _ := mustTime(nullStr(paidAt), m.TZ); ok {
				b.SetPaidAt(t)
			}
			r, err := b.Save(ctx)
			if err != nil {
				return err
			}
			m.RW.AddError("recharges", uint64(id), "1.x 单号 "+rechargeNo+" 无 2.0 列（充值单新体系）")
			if _, err := m.IDs.Put(ctx, m.Client, "recharges", uint64(id), r.ID); err != nil {
				return err
			}
			m.st.Record("recharges", "migrated")
			return nil
		},
	)
}

// migrateCoupons 1.x coupons → coupons（percent ×100 万分比；门槛进 scope meta）。
func (m *Migrator) migrateCoupons(ctx context.Context) error {
	var (
		id, minAmount                int64
		productID, categoryID        sql.NullInt64
		usedBy, orderID              sql.NullInt64
		code, ctype                  string
		value                        int64
		status                       string
		note                         sql.NullString
		expiresAt, usedAt, createdAt sql.NullString
	)
	return m.scanTable(ctx, "coupons",
		[]string{"id", "code", "type", "value", "product_id", "category_id", "min_amount",
			"status", "expires_at", "used_at", "used_by", "order_id", "note", "created_at"},
		func() []any {
			return []any{&id, &code, &ctype, &value, &productID, &categoryID, &minAmount,
				&status, &expiresAt, &usedAt, &usedBy, &orderID, &note, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "coupons", uint64(id)); ok {
				m.st.Record("coupons", "skip")
				return nil
			}
			exists, err := m.Client.Coupon.Query().Where(coupon.Code(code)).Exist(ctx)
			if err != nil {
				return err
			}
			if exists {
				m.st.Record("coupons", "skip")
				return nil
			}
			v := value
			if ctype == "percent" {
				v = value * 100 // 百分比整数 → 万分比
			}
			scope := map[string]any{}
			if pid := nullInt(productID); pid > 0 {
				if newP, ok := m.IDs.Get(ctx, "products", uint64(pid)); ok {
					scope["product_ids"] = []uint64{newP}
				}
			}
			if cid := nullInt(categoryID); cid > 0 {
				if newC, ok := m.IDs.Get(ctx, "categories", uint64(cid)); ok {
					scope["category_ids"] = []uint64{newC}
				}
			}
			if minAmount > 0 {
				scope["min_amount"] = minAmount // 2.0 无独立门槛列，scope 保真
			}
			name := nullStr(note)
			if name == "" {
				name = "v1-" + code
			}
			st := "unused"
			switch status {
			case "used":
				st = "used"
			case "disabled":
				st = "disabled"
			}
			b := m.Client.Coupon.Create().
				SetCode(code).
				SetName(name).
				SetType(coupon.Type(ctype)).
				SetValue(v).
				SetStatus(coupon.Status(st))
			if len(scope) > 0 {
				b.SetScope(scope)
			}
			if u := nullInt(usedBy); u > 0 {
				if newU, ok := m.IDs.Get(ctx, "users", uint64(u)); ok {
					b.SetUserID(newU)
				}
			}
			if o := nullInt(orderID); o > 0 {
				if newO, ok := m.IDs.Get(ctx, "orders", uint64(o)); ok {
					b.SetUsedOrderID(newO)
				}
			}
			if t, ok, _ := mustTime(nullStr(usedAt), m.TZ); ok {
				b.SetUsedAt(t)
			}
			if t, ok, _ := mustTime(nullStr(expiresAt), m.TZ); ok {
				b.SetExpireAt(t)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			c, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "coupons", uint64(id), c.ID); err != nil {
				return err
			}
			m.st.Record("coupons", "migrated")
			return nil
		},
	)
}

// migrateOrderDeliveries 1.x order_deliveries → 2.0 order_deliveries。
// card_id 定位：P3 回填映射（v1id_maps order_deliveries）或源库 (product, order) 反查。
func (m *Migrator) migrateOrderDeliveries(ctx context.Context) error {
	var (
		id                 int64
		orderID, productID sql.NullInt64
		deliveredMode      string
		deliveredAt        sql.NullString
	)
	return m.scanTable(ctx, "order_deliveries",
		[]string{"id", "order_id", "product_id", "delivered_mode", "delivered_at"},
		func() []any {
			return []any{&id, &orderID, &productID, &deliveredMode, &deliveredAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "order_deliveries_v2", uint64(id)); ok {
				m.st.Record("order_deliveries", "skip")
				return nil
			}
			newO, ok := m.IDs.Get(ctx, "orders", uint64(nullInt(orderID)))
			if !ok {
				m.st.Record("order_deliveries", "skip") // 分站订单：P6
				return nil
			}
			// card_id：优先 P3 回填映射；否则源库按 (product, order) 反查 1.x 卡
			var newCardID uint64
			if cid, ok := m.IDs.Get(ctx, "order_deliveries", uint64(id)); ok {
				newCardID = cid
			} else {
				var oldCard int64
				err := m.Src.DB.QueryRowContext(ctx,
					"SELECT id FROM `cards` WHERE `product_id` = ? AND `order_id` = ? LIMIT 1",
					nullInt(productID), nullInt(orderID)).Scan(&oldCard)
				if err == nil {
					if nc, ok := m.IDs.Get(ctx, "cards", uint64(oldCard)); ok {
						newCardID = nc
					}
				} else if err != sql.ErrNoRows {
					return err
				}
			}
			if newCardID == 0 {
				m.RW.AddError("order_deliveries", uint64(id), "无法定位 card_id（卡未迁或分站），该交付记录跳过")
				return nil
			}
			// item：该订单首 item（1.x 单商品模型）
			item, err := m.Client.OrderItem.Query().Where(orderitem.OrderIDEQ(newO)).First(ctx)
			if err != nil {
				return fmt.Errorf("订单 %d 无 item: %w", newO, err)
			}
			d, err := m.Client.OrderDelivery.Create().
				SetOrderID(newO).
				SetItemID(item.ID).
				SetCardID(newCardID).
				SetDeliveredMode(orderdelivery.DeliveredMode(deliveredMode)).
				SetDeliveryTokenHash(""). // 1.x 无取货令牌（历史单走订单查询密码取货）
				SetFetchCount(1).
				Save(ctx)
			if err != nil {
				return err
			}
			if t, ok, _ := mustTime(nullStr(deliveredAt), m.TZ); ok {
				if _, err := m.Client.OrderDelivery.UpdateOne(d).SetDeliveredAt(t).Save(ctx); err != nil {
					return err
				}
			}
			if _, err := m.IDs.Put(ctx, m.Client, "order_deliveries_v2", uint64(id), d.ID); err != nil {
				return err
			}
			m.st.Record("order_deliveries", "migrated")
			return nil
		},
	)
}

// backfillCardOrderIDs P3 置 0 的 cards.order_id 回填：
// ① 1.x cards.order_id → orders map；② order_deliveries 回填卡 → delivery.order_id → orders map。
func (m *Migrator) backfillCardOrderIDs(ctx context.Context) error {
	if m.dry {
		return nil
	}
	t := m.st.table("card_order_backfill")
	rows, err := m.Src.DB.QueryContext(ctx,
		"SELECT `id`, `order_id` FROM `cards` WHERE `order_id` > 0")
	if err != nil {
		return err
	}
	type pair struct{ card, order uint64 }
	var pairs []pair
	for rows.Next() {
		var c, o int64
		if err := rows.Scan(&c, &o); err != nil {
			return err
		}
		pairs = append(pairs, pair{uint64(c), uint64(o)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range pairs {
		newC, okC := m.IDs.Get(ctx, "cards", p.card)
		newO, okO := m.IDs.Get(ctx, "orders", p.order)
		if !okC || !okO {
			t.SkippedExists++
			continue
		}
		cardRow, err := m.Client.Card.Get(ctx, newC)
		if err != nil {
			return err
		}
		if cardRow.OrderID != 0 { // 幂等
			t.SkippedExists++
			continue
		}
		if _, err := m.Client.Card.UpdateOne(cardRow).SetOrderID(newO).Save(ctx); err != nil {
			return err
		}
		t.Migrated++
	}
	// order_deliveries 回填卡（P3 重建行）也回填 order_id
	drows, err := m.Src.DB.QueryContext(ctx,
		"SELECT `id`, `order_id` FROM `order_deliveries` WHERE `order_id` > 0")
	if err != nil {
		return err
	}
	var dpairs []pair
	for drows.Next() {
		var d, o int64
		if err := drows.Scan(&d, &o); err != nil {
			return err
		}
		dpairs = append(dpairs, pair{uint64(d), uint64(o)})
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return err
	}
	for _, p := range dpairs {
		newC, okC := m.IDs.Get(ctx, "order_deliveries", p.card)
		newO, okO := m.IDs.Get(ctx, "orders", p.order)
		if !okC || !okO {
			continue
		}
		cardRow, err := m.Client.Card.Get(ctx, newC)
		if err != nil {
			return err
		}
		if cardRow.OrderID != 0 {
			continue
		}
		if _, err := m.Client.Card.UpdateOne(cardRow).SetOrderID(newO).Save(ctx); err != nil {
			return err
		}
		t.Migrated++
	}
	return nil
}

func nullFloat(v sql.NullFloat64) float64 {
	if v.Valid {
		return v.Float64
	}
	return 0
}
