package procurement

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productdeliverysource"
	fulfillmentport "github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"math"
	"strings"
	"time"
)

// ResumeReusable is durable without Redis. A claimed submit is NEVER submitted
// again, even after a crash; unknown outcomes require reconciliation.
func (s *ProcureService) ResumeReusable(ctx context.Context) error {
	c := data.Client(ctx, s.repo.data)
	items, err := c.OrderItem.Query().Where(orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeReuse), orderitem.FulfillmentStatusNotIn("delivered", "refunded"), orderitem.HasOrderWith(order.StatusIn(order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered))).Order(ent.Asc(orderitem.FieldUpdatedAt)).Limit(100).All(ctx)
	if err != nil {
		return err
	}
	seen := map[uint64]bool{}
	for _, it := range items {
		if seen[it.DeliverySourceID] {
			continue
		}
		seen[it.DeliverySourceID] = true
		// Rotate unfinished items so one blocked source cannot starve later buyers.
		_, _ = c.OrderItem.Update().Where(orderitem.DeliverySourceID(it.DeliverySourceID), orderitem.FulfillmentStatusNotIn("delivered", "refunded")).SetUpdatedAt(time.Now().UTC()).Save(ctx)
		if err := s.processReusable(ctx, it.DeliverySourceID); err != nil {
			s.log.Warn("procurement.reuse_retry", "source_id", it.DeliverySourceID)
		}
	}
	// Also reconcile an already submitted shared purchase if all waiting buyers refunded.
	sources, err := c.ProductDeliverySource.Query().Where(productdeliverysource.StatusIn("submitting", "polling"), productdeliverysource.NextCheckAtLTE(time.Now().Unix())).Limit(20).All(ctx)
	if err != nil {
		return err
	}
	for _, src := range sources {
		if !seen[src.ID] {
			_ = s.processReusable(ctx, src.ID)
		}
	}
	return nil
}
func (s *ProcureService) processReusable(ctx context.Context, id uint64) error {
	c := data.Client(ctx, s.repo.data)
	src, err := c.ProductDeliverySource.Get(ctx, id)
	if err != nil {
		return err
	}
	if src.Status == "ready" {
		return s.deliverReusable(ctx, src)
	}
	if src.Status == "paused" || src.Status == "failed" || src.Status == "uncertain" {
		return s.failReusableItems(ctx, src.ID, "复用内容需要商家处理")
	}
	if src.NextCheckAt > time.Now().Unix() {
		return nil
	}
	if src.Status == "submitting" {
		_, err = c.ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.Status("submitting"), productdeliverysource.NextCheckAtLTE(time.Now().Unix())).SetStatus("uncertain").SetLastError("首次采购结果不明确，请核实原上游订单；不会重新采购").Save(ctx)
		return err
	}
	conn, err := c.SupplyConnection.Get(ctx, src.ConnectionID)
	if err != nil {
		return err
	}
	if data.ConnectionRevision(conn) != src.ConnectionRevision || conn.Status != "active" {
		state := "failed"
		if src.Status == "polling" {
			state = "uncertain"
		}
		return s.markReusable(ctx, id, state, "货源账号或配置已变化，请核实原采购结果", src.Status)
	}
	if src.Status == "empty" {
		if src.ExpiresAt > 0 && src.ExpiresAt <= time.Now().Unix() {
			return s.markReusable(ctx, id, "failed", "内容有效期已过，未发起采购", "empty")
		}

		claimed := false
		e := data.Tx(ctx, s.repo.data, func(txctx context.Context) error {
			tc := data.Client(txctx, s.repo.data)
			if err := tc.Product.UpdateOneID(src.ProductID).AddLockVersion(0).Exec(txctx); err != nil {
				return err
			}
			pq := tc.Product.Query().Where(product.ID(src.ProductID))
			sq := tc.ProductDeliverySource.Query().Where(productdeliverysource.ID(id))
			if s.repo.data.Dialect.Capabilities().SupportsSkipLocked {
				pq = pq.ForUpdate()
				sq = sq.ForUpdate()
			}
			if _, err := pq.Only(txctx); err != nil {
				return err
			}
			fresh, err := sq.Only(txctx)
			if err != nil {
				return err
			}
			if fresh.Status != "empty" || fresh.CurrentKey == nil {
				return nil
			}
			waiting, err := tc.OrderItem.Query().Where(orderitem.DeliverySourceID(id), orderitem.FulfillmentStatusNotIn("delivered", "refunded"), orderitem.HasOrderWith(order.StatusIn(order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered))).Exist(txctx)
			if err != nil || !waiting {
				return err
			}
			// Allocate once inside the claim transaction. This also safely initializes
			// older empty sources; a submitting source is never re-keyed or retried.
			key := fresh.PurchaseKey
			if key == nil || *key == "" {
				v := "reuse:" + uuid.NewString()
				key = &v
			}
			src.PurchaseKey = key
			n, err := tc.ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.Status("empty")).SetPurchaseKey(*key).SetStatus("submitting").SetSubmittedAt(time.Now().Unix()).SetNextCheckAt(time.Now().Add(2 * time.Minute).Unix()).Save(txctx)
			claimed = n == 1
			return err
		})
		if e != nil || !claimed {
			return e
		}
		bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		res, e := s.gw.Submit(bounded, supplyport.PurchaseRequest{ConnectionID: src.ConnectionID, ProductCode: src.UpstreamProduct, UpstreamSKU: src.UpstreamSku, Quantity: 1, DownstreamOrderNo: *src.PurchaseKey, TraceID: *src.PurchaseKey})
		if e != nil || res == nil {
			_, err = c.ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.Status("submitting")).SetStatus("uncertain").SetLastError("采购响应未确认，请核实原上游订单；不会再次扣款采购").Save(context.WithoutCancel(ctx))
			return err
		}
		if e := s.recordReusablePurchase(context.WithoutCancel(ctx), src, res.Amount, res.UpstreamOrderID); e != nil {
			return e
		}
		if res.Status == "delivered" {
			return s.confirmReusable(context.WithoutCancel(ctx), id, res.Cards, res.Amount, res.UpstreamOrderID)
		}
		if res.UpstreamOrderID == "" {
			return s.markReusable(context.WithoutCancel(ctx), id, "uncertain", "上游未返回订单号，请核实扣款和出货结果", "submitting")
		}
		_, err = c.ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.Status("submitting")).SetStatus("polling").SetNextCheckAt(time.Now().Add(15 * time.Second).Unix()).Save(context.WithoutCancel(ctx))
		return err
	}
	if src.Status == "polling" {
		n, e := c.ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.Status("polling"), productdeliverysource.NextCheckAtLTE(time.Now().Unix())).SetNextCheckAt(time.Now().Add(30 * time.Second).Unix()).Save(ctx)
		if e != nil || n == 0 {
			return e
		}
		if src.UpstreamOrderID == "" {
			return s.markReusable(ctx, id, "uncertain", "缺少原上游订单号，请核实", "polling")
		}
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		res, e := s.gw.Query(bounded, src.ConnectionID, src.UpstreamOrderID)
		if e != nil || res == nil {
			return e
		}
		switch res.Status {
		case "delivered":
			return s.confirmReusable(ctx, id, res.Cards, res.Amount, src.UpstreamOrderID)
		case "failed", "rejected", "canceled", "refunded":
			return s.markReusable(ctx, id, "failed", "上游采购未完成，相关订单待处理；不会自动退整单", "polling")
		}
		if src.SubmittedAt > 0 && time.Now().Unix()-src.SubmittedAt > 86400 {
			return s.markReusable(ctx, id, "uncertain", "采购长时间未完成，请核实原订单", "polling")
		}
	}
	return nil
}
func (s *ProcureService) confirmReusable(ctx context.Context, id uint64, cards []string, amount int64, upstreamID string) error {
	c := data.Client(ctx, s.repo.data)
	src, err := c.ProductDeliverySource.Get(ctx, id)
	if err != nil {
		return err
	}
	if e := s.recordReusablePurchase(ctx, src, amount, upstreamID); e != nil {
		return e
	}
	if src.Status == "ready" {
		return s.deliverReusable(ctx, src)
	}
	if src.Status != "submitting" && src.Status != "polling" && src.Status != "uncertain" {
		return nil
	}
	if len(cards) != 1 || strings.TrimSpace(cards[0]) == "" || len(cards[0]) > 60000 {
		return s.markReusable(ctx, id, "uncertain", "上游返回内容不是单份，请核实后选择可复用的内容", "submitting", "polling", "uncertain")
	}
	sealed, err := s.cipher.Seal(cards[0], src.ProductID, src.SubsiteID)
	if err != nil {
		return err
	}
	n, err := c.ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.StatusIn("submitting", "polling", "uncertain")).SetContent(sealed).SetStatus("ready").SetLastError("").Save(ctx)
	if err != nil || n == 0 {
		return err
	}
	src, err = c.ProductDeliverySource.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.deliverReusable(ctx, src)
}
func (s *ProcureService) deliverReusable(ctx context.Context, src *ent.ProductDeliverySource) error {
	c := data.Client(ctx, s.repo.data)
	items, err := c.OrderItem.Query().Where(orderitem.DeliverySourceID(src.ID), orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeReuse), orderitem.FulfillmentStatusNotIn("delivered", "refunded"), orderitem.HasOrderWith(order.StatusIn(order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered))).Order(ent.Asc(orderitem.FieldUpdatedAt), ent.Asc(orderitem.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return err
	}
	delivery, ok := s.attach.(fulfillmentport.ReusableDelivery)
	if !ok {
		return fmt.Errorf("共享交付组件未配置")
	}
	var firstErr error
	for _, it := range items {
		_ = c.OrderItem.UpdateOneID(it.ID).SetUpdatedAt(time.Now().UTC()).Exec(ctx)
		if err := delivery.DeliverReusable(ctx, it.OrderID, it.ID, src.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
func (s *ProcureService) failReusableItems(ctx context.Context, id uint64, reason string) error {
	c := data.Client(ctx, s.repo.data)
	_, err := c.OrderItem.Update().Where(orderitem.DeliverySourceID(id), orderitem.FulfillmentStatus("pending"), orderitem.HasOrderWith(order.StatusIn(order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered))).SetFulfillmentStatus("failed").Save(ctx)
	return err
}

// Capture the supplier exchange rate when the source is configured, not at query time.
func sharedPurchaseCost(amount int64, rate float64) int64 {
	if amount <= 0 || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	v := decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(rate)).Round(0)
	if v.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0
	}
	return v.IntPart()
}

func (s *ProcureService) markReusable(ctx context.Context, id uint64, state, reason string, expected ...string) error {
	_, e := data.Client(ctx, s.repo.data).ProductDeliverySource.Update().Where(productdeliverysource.ID(id), productdeliverysource.StatusIn(expected...)).SetStatus(state).SetLastError(reason).Save(ctx)
	return e
}

// Callbacks can precede the submit response. Fill missing metadata without
// changing a completed receipt's state, content, or already recorded amount.
func (s *ProcureService) recordReusablePurchase(ctx context.Context, src *ent.ProductDeliverySource, amount int64, upstreamID string) error {
	c := data.Client(ctx, s.repo.data)
	if upstreamID != "" {
		if _, e := c.ProductDeliverySource.Update().Where(productdeliverysource.ID(src.ID), productdeliverysource.UpstreamOrderID("")).SetUpstreamOrderID(upstreamID).Save(ctx); e != nil {
			return e
		}
	}
	if cost := sharedPurchaseCost(amount, src.ExchangeRate); cost > 0 {
		if _, e := c.ProductDeliverySource.Update().Where(productdeliverysource.ID(src.ID), productdeliverysource.CostCents(0)).SetCostCents(cost).Save(ctx); e != nil {
			return e
		}
	}
	return nil
}
