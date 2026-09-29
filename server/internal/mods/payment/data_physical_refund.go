package payment

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// Physical refunds allocate principal, shipping and canceled obligations separately.
// All checks, money, stock, progress and the durable event commit atomically.
func (r *PaymentRepoImpl) RefundPhysical(ctx context.Context, oid, actor uint64, req *adminv1.CreateRefundRequest) (*ent.RefundOrder, error) {
	var allocations []map[string]int64
	if json.Unmarshal([]byte(req.ItemAllocationsJson), &allocations) != nil || len(allocations) > 100 || req.ExpectedRefundedCents == nil || !money.ValidCents(req.AmountCents) || !money.ValidCents(req.FeeCents) || len([]rune(req.Reason)) > 180 {
		return nil, refundInvalid("请填写有效的商品退款分摊并刷新订单金额")
	}
	noMoney := req.AmountCents == 0 && req.FeeCents == 0
	if (noMoney && req.RequestKey == "") || (req.RequestKey != "" && (len(req.RequestKey) < 8 || len(req.RequestKey) > 100)) {
		return nil, refundInvalid("请刷新页面后重试，取消数量必须携带有效请求标识")
	}
	var requestKey, requestHash string
	if req.RequestKey != "" {
		requestKey = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", oid, req.RequestKey))))
		sort.Slice(allocations, func(i, j int) bool { return allocations[i]["item_id"] < allocations[j]["item_id"] })
		raw, _ := json.Marshal([]any{allocations, req.AmountCents, req.FeeCents, req.Channel, req.Reason, req.ExternalConfirmed, req.ExternalReference, req.ExpectedRefundedCents, req.ExpectedRefundedFeeCents})
		requestHash = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	externalReference := strings.TrimSpace(req.ExternalReference)
	if noMoney {
		externalReference = ""
	}
	if req.Channel != "wallet" && !(req.Channel == "gateway" && (noMoney || req.ExternalConfirmed && strings.TrimSpace(req.ExternalReference) != "" && len(req.ExternalReference) <= 64)) {
		return nil, refundInvalid("请选择退至余额，或核实线下退款成功后填写退款凭证")
	}
	var result *ent.RefundOrder
	err := data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		o, e := c.Order.Get(ctx, oid)
		if e != nil {
			return e
		}
		if o.CommerceVersion != 1 {
			return refundInvalid("订单版本不支持商品退款")
		}
		if requestKey != "" {
			previous, err := c.RefundOrder.Query().Where(refundorder.RequestKey(requestKey)).Only(ctx)
			if err == nil {
				if previous.OrderID != oid || previous.RequestHash != requestHash {
					return refundInvalid("同一退款请求不能更改内容，请刷新核对后重新操作")
				}
				result = previous
				return nil
			}
			if !ent.IsNotFound(err) {
				return err
			}
		}
		if req.Channel == "wallet" && o.UserID == 0 && !noMoney {
			return refundInvalid("游客订单不能退至会员余额")
		}
		switch o.Status {
		case order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted, order.StatusRefunded:
		default:
			return refundInvalid("当前订单状态不支持退款")
		}
		receipts, e := c.RefundOrder.Query().Where(refundorder.OrderID(oid)).All(ctx)
		if e != nil {
			return e
		}
		var refunded, feeRefunded int64
		for _, rf := range receipts {
			if !noMoney && req.Channel == "gateway" && rf.Channel == refundorder.ChannelGateway && strings.TrimSpace(rf.UpstreamRefundID) == externalReference && rf.Status != refundorder.StatusFailed {
				return refundInvalid("该外部退款凭证已登记，请核对退款记录")
			}
			if rf.Status == refundorder.StatusCreated || rf.Status == refundorder.StatusProcessing {
				return refundInvalid("存在待核对退款，请先核对")
			}
			if rf.Status == refundorder.StatusSucceeded {
				refunded += rf.Amount
				feeRefunded += rf.FeeAmount
			}
		}
		if refunded != req.GetExpectedRefundedCents() || refunded > o.TotalAmount || req.AmountCents > o.TotalAmount-refunded {
			return refundInvalid("可退金额已变化，请刷新后确认")
		}
		paid, e := c.Payment.Query().Where(payment.OrderID(oid), payment.StatusEQ(payment.StatusSuccess), payment.ReviewReasonEQ("")).Order(ent.Asc(payment.FieldID)).First(ctx)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		var fee int64
		if paid != nil {
			fee = paid.Fee
		}
		if req.FeeCents > fee-feeRefunded || (req.FeeCents > 0 && req.ExpectedRefundedFeeCents == nil) || (req.ExpectedRefundedFeeCents != nil && req.GetExpectedRefundedFeeCents() != feeRefunded) {
			return refundInvalid("支付手续费可退金额已变化")
		}
		// Order CAS uses the same lock as shipping and digital delivery.
		n, e := c.Order.Update().Where(order.ID(oid), order.Version(o.Version), order.StatusEQ(o.Status)).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return refundInvalid("订单已变化，请刷新重试")
		}
		items, e := c.OrderItem.Query().Where(orderitem.OrderID(oid)).All(ctx)
		if e != nil {
			return e
		}
		byID := map[uint64]*ent.OrderItem{}
		for _, it := range items {
			byID[it.ID] = it
		}
		seen := map[uint64]bool{}
		var sum, shippingSum int64
		var canceled int64
		for _, a := range allocations {
			id := uint64(a["item_id"])
			it := byID[id]
			amount, ship, qty := a["amount_cents"], a["shipping_cents"], a["cancel_quantity"]
			if it == nil || seen[id] || !money.ValidCents(amount) || !money.ValidCents(ship) || qty < 0 || qty > int64(it.Quantity-it.CanceledQuantity) {
				return refundInvalid("商品退款分摊无效")
			}
			seen[id] = true
			if amount > it.PaidAmount-it.RefundedAmount || ship > it.ShippingAmount-it.RefundedShipping {
				return refundInvalid("商品或运费退款超过剩余可退金额")
			}
			if (amount > 0 || qty > 0) && it.FulfillmentType == orderitem.FulfillmentTypeUpstream && it.FulfillmentStatus != "delivered" {
				busy, err := c.ProcurementOrder.Query().Where(procurementorder.OrderItemID(it.ID), procurementorder.StatusNotIn("rejected", "refunded")).Exist(ctx)
				if err != nil {
					return err
				}
				if busy {
					return refundInvalid("上游采购已提交或结果待核对，请先核实采购结果再退款")
				}
			}
			if qty > 0 {
				if it.GoodsType == "physical" {
					if qty > int64(it.Quantity-it.CanceledQuantity-it.ShippedQuantity) {
						return refundInvalid("已发货数量不能取消，退货需另行验收入库")
					}
				} else {
					if qty != int64(it.Quantity-it.CanceledQuantity) || it.FulfillmentStatus != "pending" {
						return refundInvalid("虚拟商品只能整项取消尚未交付且尚未采购的数量")
					}
				}
			}
			if sum > money.MaxCents-amount-ship {
				return refundInvalid("退款金额超限")
			}
			sum += amount + ship
			shippingSum += ship
			canceled += qty
		}
		if o.TotalAmount > 0 && refunded+sum == o.TotalAmount {
			for _, it := range items {
				var cancel int64
				for _, a := range allocations {
					if uint64(a["item_id"]) == it.ID {
						cancel = a["cancel_quantity"]
					}
				}
				remaining := it.Quantity - it.CanceledQuantity
				if it.GoodsType == "physical" {
					remaining -= it.ShippedQuantity
				} else if it.FulfillmentStatus == "delivered" || it.FulfillmentStatus == "refunded" {
					remaining = 0
				}
				if int64(remaining) > cancel {
					return refundInvalid("退清整单金额时，请同时取消所有尚未交付的商品数量")
				}
			}
		}
		if sum != req.AmountCents || (sum == 0 && req.FeeCents == 0 && canceled == 0) {
			return refundInvalid("退款金额与商品分摊不一致，或没有退款/取消内容")
		}
		create := c.RefundOrder.Create().SetOrderID(oid).SetAmount(sum).SetShippingAmount(shippingSum).SetFeeAmount(req.FeeCents).SetItemAllocations(allocations).SetChannel(refundorder.Channel(req.Channel)).SetStatus(refundorder.StatusSucceeded).SetOperatorID(actor).SetReason(req.Reason).SetUpstreamRefundID(externalReference)
		if requestKey != "" {
			create.SetRequestKey(requestKey).SetRequestHash(requestHash)
		}
		result, e = create.Save(ctx)
		if e != nil {
			return e
		}
		if req.Channel == "wallet" && sum+req.FeeCents > 0 {
			if r.wallet == nil {
				return refundInvalid("余额退款服务不可用")
			}
			if e = r.wallet.CreditInTx(ctx, walletport.Entry{UserID: o.UserID, Direction: "in", Type: "refund", Amount: money.Cents(sum + req.FeeCents), OrderID: oid, Reference: fmt.Sprintf("order_refund:%d", result.ID), Remark: "订单 " + o.OrderNo + " 商品退款"}); e != nil {
				return e
			}
		}
		for _, a := range allocations {
			it := byID[uint64(a["item_id"])]
			qty := int32(a["cancel_quantity"])
			q := c.OrderItem.UpdateOneID(it.ID).AddRefundedAmount(a["amount_cents"]).AddRefundedShipping(a["shipping_cents"]).AddCanceledQuantity(qty)
			if it.CanceledQuantity+qty == it.Quantity {
				q.SetFulfillmentStatus("refunded")
			}
			if e = q.Exec(ctx); e != nil {
				return e
			}
			if qty > 0 && it.GoodsType == "physical" {
				if e = data.MovePhysicalStock(ctx, r.data, it.SubsiteID, it.ProductID, it.SkuID, oid, int64(qty), fmt.Sprintf("refund:%d:%d", result.ID, it.ID), "退款取消未发货数量"); e != nil {
					return e
				}
			}
			if qty > 0 && it.GoodsType != "physical" {
				skuFilter := card.SkuID(it.SkuID)
				if it.SkuID == 0 {
					skuFilter = card.Or(card.SkuID(0), card.SkuIDIsNil())
				}
				if _, e = c.Card.Update().Where(card.OrderID(oid), card.ProductID(it.ProductID), skuFilter, card.StatusEQ(card.StatusReserved)).SetStatus(card.StatusAvailable).ClearOrderID().ClearLockedAt().Save(ctx); e != nil {
					return e
				}
			}
		}
		progressOrder := *o
		if o.TotalAmount > 0 && refunded+sum == o.TotalAmount {
			if e = c.Order.UpdateOneID(oid).SetStatus(order.StatusRefunded).Exec(ctx); e != nil {
				return e
			}
			progressOrder.Status = order.StatusRefunded
		}
		if e = data.RefreshPhysicalProgress(ctx, r.data, &progressOrder); e != nil {
			return e
		}
		if e = data.PhysicalOrderEvent(ctx, r.data, o, "item_refund", "admin", actor, fmt.Sprintf("退款凭证 %d；商品 %d 分；运费 %d 分；支付手续费 %d 分；%s", result.ID, sum-shippingSum, shippingSum, req.FeeCents, req.Reason)); e != nil {
			return e
		}
		if r.outbox == nil {
			return refundInvalid("退款事件服务不可用")
		}
		raw, _ := json.Marshal(map[string]any{"order_id": oid, "order_no": o.OrderNo, "refund_id": result.ID, "commerce_version": 1, "amount": sum, "shipping_amount": shippingSum, "user_id": o.UserID})
		return r.outbox.Write(ctx, "order", events.OrderRefunded, o.OrderNo, fmt.Sprintf("order:%s:refund:%d", o.OrderNo, result.ID), raw)
	})
	return result, err
}
