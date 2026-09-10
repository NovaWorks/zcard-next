package fulfillment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
)

// ManualDeliver supplies one outstanding item. Legacy single-item callers may omit itemID.
func (r *DeliveryRepoImpl) ManualDeliver(ctx context.Context, orderNo, content, logisticsNo, remark string, adminID uint64, itemID ...uint64) error {
	content, logisticsNo = strings.TrimSpace(content), strings.TrimSpace(logisticsNo)
	if (content == "") == (logisticsNo == "") {
		return fmt.Errorf("卡密内容与物流单号必须且只能填写一项")
	}
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		client := data.Client(ctx, r.data)
		o, err := client.Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
		if err != nil {
			return err
		}
		if err := r.lockDeliveryOrder(ctx, o); err != nil {
			return err
		}
		processing, err := client.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusEQ(refundorder.StatusProcessing)).Exist(ctx)
		if err != nil {
			return err
		}
		if processing {
			return fmt.Errorf("退款处理中，请先核实退款结果")
		}
		its, err := client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).Order(ent.Asc(orderitem.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		var target *ent.OrderItem
		var selected uint64
		if len(itemID) > 0 {
			selected = itemID[0]
		}
		if selected == 0 && len(its) == 1 {
			target = its[0]
		} else {
			for _, it := range its {
				if it.ID == selected {
					target = it
					break
				}
			}
		}
		if target == nil {
			return fmt.Errorf("请选择本订单中需要补发的商品")
		}
		counts, err := r.deliveredQuantities(ctx, o.ID, its)
		if err != nil {
			return err
		}
		remaining := int(target.Quantity) - counts[target.ID]
		if remaining <= 0 {
			return fmt.Errorf("该商品已发货，请刷新订单")
		}
		var lines []string
		for _, line := range strings.Split(content, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		if content != "" && len(lines) != remaining {
			return fmt.Errorf("该商品待补发 %d 条，请每行填写一条卡密", remaining)
		}

		po, err := client.ProcurementOrder.Query().Where(procurementorder.OrderItemID(target.ID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if po != nil {
			switch po.Status {
			case procurementorder.StatusManual, procurementorder.StatusRejected, procurementorder.StatusRefunding:
			default:
				return fmt.Errorf("上游采购仍在进行或已交付，请先在采购单核实并转人工，避免重复发货")
			}
			// Also supports old refunding receipts whose executor never ran.
			n, err := client.ProcurementOrder.Update().Where(procurementorder.ID(po.ID), procurementorder.StatusEQ(po.Status)).SetStatus(procurementorder.StatusFulfilled).SetLastError("管理员已人工补发，原采购异常已处理").Save(ctx)
			if err != nil {
				return err
			}
			if n != 1 {
				return fmt.Errorf("采购单已变化，请刷新后重试")
			}
		}
		now := time.Now().UTC()
		createDelivery := func(cardID uint64, logistics map[string]any) error {
			b := client.OrderDelivery.Create().SetOrderID(o.ID).SetItemID(target.ID).SetCardID(cardID).SetDeliveryTokenHash(hashToken(randomToken())).SetDeliveredMode(orderdelivery.DeliveredModeStatus).SetDeliveredBy(adminID).SetFetchCount(0).SetDeliveredAt(now)
			if logistics != nil {
				b.SetLogistics(logistics)
			}
			return b.Exec(ctx)
		}
		for _, line := range lines {
			sealed, err := r.cipher.Seal(line, target.ProductID, o.SubsiteID)
			if err != nil {
				return err
			}
			c, err := client.Card.Create().SetProductID(target.ProductID).SetSkuID(target.SkuID).SetSubsiteID(o.SubsiteID).SetContent(sealed).SetContentHash(r.cipher.ContentHash(line)).SetStatus(card.StatusUsed).SetOrderID(o.ID).SetUsedAt(now).Save(ctx)
			if err != nil {
				return err
			}
			if err := createDelivery(c.ID, nil); err != nil {
				return err
			}
		}
		if logisticsNo != "" {
			if err := createDelivery(0, map[string]any{"tracking_no": logisticsNo, "remark": remark}); err != nil {
				return err
			}
		}
		// Release local reserved cards replaced by manual delivery, not unrelated items.
		if _, err := client.Card.Update().Where(card.OrderID(o.ID), card.ProductID(target.ProductID), card.SkuID(target.SkuID), card.StatusEQ(card.StatusReserved)).SetStatus(card.StatusAvailable).ClearOrderID().ClearLockedAt().Save(ctx); err != nil {
			return err
		}
		if _, err := client.RefundOrder.Update().Where(refundorder.OrderID(o.ID), refundorder.StatusEQ(refundorder.StatusCreated), refundorder.ChannelEQ(refundorder.ChannelUpstream)).SetStatus(refundorder.StatusFailed).SetReason("旧自动退款未执行，管理员已选择人工补发").Save(ctx); err != nil {
			return err
		}
		return r.updateDeliveryProgress(ctx, o, "admin", adminID, remark)
	})
}
