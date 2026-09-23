package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func refundInvalid(message string) error {
	return kerrors.BadRequest("payment.REFUND_INVALID", message)
}

// RefundToWallet commits the receipt, balance, order state and outbox together.
// expectedRefunded is the amount the administrator saw before confirming; stale
// or repeated submissions cannot issue a second credit.
type RefundFeeInput struct {
	Amount   int64
	Expected *int64
}

func (r *PaymentRepoImpl) RefundToWallet(ctx context.Context, orderID uint64, amount int64, expectedRefunded *int64, reason string, operatorID uint64, fees ...RefundFeeInput) (*ent.RefundOrder, error) {
	feeAmount := int64(0)
	var expectedFee *int64
	if len(fees) > 0 {
		feeAmount = fees[0].Amount
		expectedFee = fees[0].Expected
	}
	if !money.ValidCents(amount) || !money.ValidCents(feeAmount) || amount+feeAmount <= 0 || !money.ValidCents(amount+feeAmount) {
		return nil, refundInvalid("退款本金和手续费不能为负，合计必须大于 0 且不超限")
	}
	if expectedRefunded == nil {
		return nil, refundInvalid("请刷新订单详情后重新确认退款金额")
	}
	if len([]rune(reason)) > 180 {
		return nil, refundInvalid("退款原因不能超过 180 字")
	}
	var result *ent.RefundOrder
	err := data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		o, err := c.Order.Get(ctx, orderID)
		if err != nil {
			return err
		}
		if o.UserID == 0 {
			return refundInvalid("游客订单没有会员余额，不能退款到钱包，请联系买家核实退款方式")
		}
		receipts, err := c.RefundOrder.Query().Where(refundorder.OrderID(orderID)).All(ctx)
		if err != nil {
			return err
		}
		var refunded, refundedFee int64
		for _, rf := range receipts {
			if rf.Status == refundorder.StatusSucceeded {
				if rf.Amount < 0 || rf.Amount > o.TotalAmount-refunded {
					return refundInvalid("历史退款金额异常，请核对退款记录")
				}
				refunded += rf.Amount
				if rf.FeeAmount < 0 || refundedFee > money.MaxCents-rf.FeeAmount {
					return refundInvalid("历史手续费退款金额异常")
				}
				refundedFee += rf.FeeAmount
			}
			if rf.Status == refundorder.StatusProcessing || (rf.Status == refundorder.StatusCreated && rf.Channel != refundorder.ChannelWallet) {
				return refundInvalid("存在待核对的其他渠道退款，请先核对，避免重复退款")
			}
		}
		paid, payErr := c.Payment.Query().Where(payment.OrderID(o.ID), payment.StatusEQ(payment.StatusSuccess), payment.ReviewReasonEQ("")).Order(ent.Asc(payment.FieldID)).First(ctx)
		if payErr != nil && !ent.IsNotFound(payErr) {
			return payErr
		}
		totalFee := int64(0)
		if payErr == nil {
			totalFee = paid.Fee
		}
		if refundedFee > totalFee || feeAmount > totalFee-refundedFee {
			return refundInvalid("手续费退款超过剩余可退金额")
		}
		if (feeAmount > 0 && expectedFee == nil) || (expectedFee != nil && *expectedFee != refundedFee) {
			return refundInvalid("手续费退款金额已变化，请刷新后确认")
		}
		if refunded != *expectedRefunded {
			return refundInvalid("退款金额已变化，请刷新订单详情核对，勿重复退款")
		}
		switch o.Status {
		case order.StatusRefunded:
			if amount != 0 {
				return refundInvalid("订单本金已退清，只能退还剩余手续费")
			}
		case order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted:
		default:
			return refundInvalid("当前订单状态不支持退款")
		}
		if amount > o.TotalAmount-refunded {
			return refundInvalid("退款金额超过订单剩余可退金额")
		}
		full := amount > 0 && refunded+amount == o.TotalAmount
		if !full {
			pending, e := c.OrderItem.Query().Where(orderitem.OrderID(o.ID), orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeManual), orderitem.FulfillmentStatusNotIn("delivered", "refunded")).Exist(ctx)
			if e != nil {
				return e
			}
			if pending {
				return refundInvalid("人工服务未完成，暂不支持无法归属到商品项的部分退款")
			}
		}
		// Existing commission reversal consumers are order-level, so such orders must
		// be refunded in full until per-refund commission allocation is available.
		if !full && amount > 0 {
			hasCommission, err := c.AffiliateCommission.Query().Where(affiliatecommission.OrderID(orderID)).Exist(ctx)
			if err != nil {
				return err
			}
			if hasCommission || o.SubsiteProfit > 0 {
				return refundInvalid("该订单有关联佣金或分站利润，请使用全额退款")
			}
		}
		if r.wallet == nil || r.outbox == nil {
			return kerrors.ServiceUnavailable("payment.REFUND_UNAVAILABLE", "退款服务暂不可用")
		}
		// Version CAS serializes refunds with other order changes before any credit.
		update := c.Order.Update().Where(order.ID(o.ID), order.Version(o.Version), order.StatusEQ(o.Status)).AddVersion(1)
		next := o.Status
		if full {
			next = order.StatusRefunded
			update.SetStatus(next)
		}
		n, err := update.Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return refundInvalid("订单状态已变化，请刷新后核对退款结果")
		}
		result, err = c.RefundOrder.Create().SetOrderID(orderID).SetAmount(amount).SetFeeAmount(feeAmount).SetChannel(refundorder.ChannelWallet).SetStatus(refundorder.StatusSucceeded).SetOperatorID(operatorID).SetReason(reason).Save(ctx)
		if err != nil {
			return err
		}
		reference := fmt.Sprintf("order_refund:%d", result.ID)
		if err = r.wallet.CreditInTx(ctx, walletport.Entry{UserID: o.UserID, Direction: "in", Type: "refund", Amount: money.Cents(amount + feeAmount), Reference: reference, Remark: "订单 " + o.OrderNo + " 退款"}); err != nil {
			return err
		}
		// Old wallet rows were intents only; supersede them after an actual credit.
		if _, err = c.RefundOrder.Update().Where(refundorder.OrderID(orderID), refundorder.ChannelEQ(refundorder.ChannelWallet), refundorder.StatusEQ(refundorder.StatusCreated)).SetStatus(refundorder.StatusFailed).SetReason("旧退款申请未执行，已由新的余额退款记录替代").Save(ctx); err != nil {
			return err
		}
		event := "refund_partial"
		if amount == 0 {
			event = "refund_fee"
		}
		if full {
			event = "refunded"
		}
		audit := fmt.Sprintf("退至会员余额：本金 %d 分，手续费 %d 分；累计本金 %d 分，手续费 %d 分", amount, feeAmount, refunded+amount, refundedFee+feeAmount)
		if strings.TrimSpace(reason) != "" {
			audit += "；" + reason
		}
		if _, err = c.OrderStatusEvent.Create().SetOrderID(orderID).SetFromStatus(string(o.Status)).SetToStatus(string(next)).SetEvent(event).SetOperator("admin").SetOperatorID(operatorID).SetReason(audit).Save(ctx); err != nil {
			return err
		}
		if full {
			if _, err = c.OrderItem.Update().Where(orderitem.OrderID(orderID)).SetFulfillmentStatus("refunded").Save(ctx); err != nil {
				return err
			}
			if _, err = c.Card.Update().Where(card.OrderID(orderID), card.StatusEQ(card.StatusReserved)).SetStatus(card.StatusAvailable).ClearOrderID().ClearLockedAt().Save(ctx); err != nil {
				return err
			}
			raw, _ := json.Marshal(map[string]any{"order_id": orderID, "order_no": o.OrderNo, "refund_id": result.ID, "refund_ratio": 10000, "amount": amount, "user_id": o.UserID})
			if err = r.outbox.Write(ctx, "order", events.OrderRefunded, o.OrderNo, "order:"+o.OrderNo+":refunded", raw); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
