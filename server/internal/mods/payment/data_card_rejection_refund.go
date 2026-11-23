package payment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
)

type cardPurchaseCipher interface {
	Open([]byte, uint64, uint64) (string, error)
}

func (r *PaymentRepoImpl) SetCardPurchaseCipher(cipher cardPurchaseCipher) {
	r.cardPurchaseCipher = cipher
}

func (r *PaymentRepoImpl) validateCardRejection(po *ent.ProcurementOrder, it *ent.OrderItem, part *ent.ProcurementItem, driver string) error {
	if r.cardPurchaseCipher == nil || len(it.UpstreamCardCipher) == 0 {
		return refundInvalid("采购冻结报价解密不可用")
	}
	raw, e := r.cardPurchaseCipher.Open(it.UpstreamCardCipher, it.ID, it.SubsiteID)
	if e != nil {
		return refundInvalid("采购冻结报价无法核对")
	}
	var f struct {
		Request supplyport.CardPurchaseRequest `json:"request"`
		Quote   supplyport.CardPurchaseQuote   `json:"quote"`
	}
	if json.Unmarshal([]byte(raw), &f) != nil || f.Request.ProductID != it.ProductID || f.Request.SKUID != it.SkuID || f.Request.Quantity != int(it.Quantity) || f.Request.ConnectionID != po.ConnectionID || f.Request.ProductCode != part.UpstreamSku || f.Quote.CostCents <= 0 || data.SnapshotInt(it.ProfitSnapshot["card_cost_total_cents"]) != f.Quote.CostCents {
		return refundInvalid("采购冻结报价身份或成本不匹配")
	}
	b, e := json.Marshal(po.UpstreamSourceSnapshot[cardpurchase.RejectionSnapshotKey])
	if e != nil {
		return e
	}
	var proof cardpurchase.RejectionProof
	if json.Unmarshal(b, &proof) != nil || proof.ValidateReceipt(driver) != nil {
		return refundInvalid("采购拒绝凭证无效")
	}
	if proof.QuoteID != f.Quote.ProviderQuoteID || proof.Currency != f.Quote.ProviderCurrency || proof.NativeCapCents != f.Quote.ProviderAmountCents || proof.ProductID != it.ProductID || proof.SKUID != it.SkuID || proof.ConnectionID != po.ConnectionID || proof.Quantity != it.Quantity || proof.ProductCode != f.Request.ProductCode || proof.UpstreamSKU != f.Request.UpstreamSKU || proof.ConfigurationHash != f.Quote.ConfigurationHash || proof.FrozenCipherHash != fmt.Sprintf("%x", sha256.Sum256(it.UpstreamCardCipher)) {
		return refundInvalid("采购拒绝凭证与冻结购买不匹配")
	}
	sealed, e := base64.StdEncoding.DecodeString(proof.Seal)
	if e != nil || len(sealed) == 0 {
		return refundInvalid("采购拒绝凭证认证缺失")
	}
	verified, e := r.cardPurchaseCipher.Open(sealed, it.ID, it.SubsiteID)
	if e != nil {
		return refundInvalid("采购拒绝凭证认证失败")
	}
	canonical, e := proof.SealPayload()
	if e != nil {
		return e
	}
	if !bytes.Equal([]byte(verified), append([]byte(cardpurchase.RejectionSealPrefix), canonical...)) {
		return refundInvalid("采购拒绝凭证被修改")
	}
	return nil
}

// RefundRejectedCard is deliberately narrower than the generic gateway refund
// entry point. A successful wallet payment and an authoritative zero-acceptance
// stock rejection are required; remote/unknown/delivered purchases stay manual.
func (r *PaymentRepoImpl) RefundRejectedCard(ctx context.Context, procurementID uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		if e := c.ProcurementOrder.UpdateOneID(procurementID).AddRetryCount(0).Exec(ctx); e != nil {
			return e
		}
		po, e := c.ProcurementOrder.Get(ctx, procurementID)
		if e != nil {
			return e
		}
		key := fmt.Sprintf("card-rejection:%d", po.ID)
		if po.Status == "refunded" {
			receipt, e := c.RefundOrder.Query().Where(refundorder.RequestKey(key), refundorder.StatusEQ(refundorder.StatusSucceeded)).Only(ctx)
			if e != nil || receipt.Amount <= 0 {
				return refundInvalid("采购退款凭证缺失")
			}
			return nil
		}
		proved := po.LastError == supplyport.ErrCardPurchaseRejected.Error()
		if po.Status != "refunding" || po.FailStrategy != "auto_refund" || po.UpstreamOrderID != "" || !proved && po.LastError != supplyport.ErrUpstreamNoStock.Error() {
			return refundInvalid("采购结果尚未证明可自动退款")
		}
		conn, e := c.SupplyConnection.Get(ctx, po.ConnectionID)
		if e != nil || conn.Driver != "zcard" && (!proved || conn.Driver != "acg_faka") {
			return refundInvalid("该采购需人工核对退款")
		}
		parts, e := c.ProcurementItem.Query().Where(procurementitem.ProcurementID(po.ID)).All(ctx)
		if e != nil {
			return e
		}
		if len(parts) != 1 {
			return refundInvalid("采购明细不完整")
		}
		for _, part := range parts {
			if len(part.ReceivedContent) > 0 {
				return refundInvalid("采购已有交付回执")
			}
		}
		it, e := c.OrderItem.Get(ctx, po.OrderItemID)
		if e != nil {
			return e
		}
		if it.Quantity <= 0 || parts[0].Quantity != it.Quantity {
			return refundInvalid("采购数量与订单不符")
		}
		if proved {
			if e = r.validateCardRejection(po, it, parts[0], conn.Driver); e != nil {
				return e
			}
		} else if len(it.UpstreamCardCipher) > 0 {
			return refundInvalid("动态采购需要完整拒绝凭证")
		}
		o, e := c.Order.Get(ctx, it.OrderID)
		if e != nil {
			return e
		}
		items, e := c.OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
		if e != nil {
			return e
		}
		if len(items) != 1 || items[0].ID != it.ID || it.FulfillmentType != "upstream" || it.DeliveryKind != "card" || it.FulfillmentStatus == "delivered" || it.FulfillmentStatus == "refunded" || it.CanceledQuantity != 0 || o.UserID == 0 || o.BaseCurrency != "CNY" || o.CommerceVersion != 0 || o.TotalAmount <= 0 || o.Status != order.StatusPaid && o.Status != order.StatusFulfilling {
			return refundInvalid("该订单需人工按商品项核对退款")
		}
		paid, e := c.Payment.Query().Where(payment.OrderID(o.ID), payment.StatusEQ(payment.StatusSuccess), payment.ReviewReason("")).Only(ctx)
		if e != nil || paid.DriverSnapshot != "wallet" || paid.Amount != o.TotalAmount || paid.Fee != 0 {
			return refundInvalid("需要原始余额付款成功凭证")
		}
		receipts, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusNEQ(refundorder.StatusFailed)).Exist(ctx)
		if e != nil {
			return e
		}
		if receipts {
			return refundInvalid("已有其他退款，需人工核对")
		}
		refunded := int64(0)
		receipt, e := r.RefundToWallet(ctx, o.ID, o.TotalAmount, &refunded, "上游采购已明确拒绝且无净扣款，退回原付款余额", 0)
		if e != nil {
			return e
		}
		if e = c.RefundOrder.UpdateOneID(receipt.ID).SetRequestKey(key).Exec(ctx); e != nil {
			return e
		}
		if e = c.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(o.TotalAmount).SetCanceledQuantity(it.Quantity).SetFulfillmentStatus("refunded").Exec(ctx); e != nil {
			return e
		}
		return c.ProcurementOrder.UpdateOneID(po.ID).SetStatus("refunded").SetUpstreamRefundID(fmt.Sprintf("wallet:%d", receipt.ID)).Exec(ctx)
	})
}
