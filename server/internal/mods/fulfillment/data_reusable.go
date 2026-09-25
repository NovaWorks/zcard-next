package fulfillment

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productdeliverysource"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"time"
)

func (r *DeliveryRepoImpl) DeliverReusable(ctx context.Context, orderID, itemID, sourceID uint64) error {
	c := data.Client(ctx, r.data)
	it, err := c.OrderItem.Get(ctx, itemID)
	if err != nil {
		return err
	}
	if it.OrderID != orderID || it.DeliverySourceID != sourceID || it.FulfillmentType != orderitem.FulfillmentTypeReuse {
		return fmt.Errorf("共享交付来源不匹配")
	}
	o, err := c.Order.Get(ctx, orderID)
	if err != nil {
		return err
	}
	return r.FulfillOrder(ctx, o.OrderNo)
}

// Called with the order locked. A source reference, rather than a duplicate card
// row, preserves card-pool deduplication and immutable historical deliveries.
func (r *DeliveryRepoImpl) fulfillReusable(ctx context.Context, o *ent.Order, it *ent.OrderItem) error {
	c := data.Client(ctx, r.data)
	done, err := c.OrderDelivery.Query().Where(orderdelivery.OrderID(o.ID), orderdelivery.ItemID(it.ID)).Exist(ctx)
	if err != nil || done {
		return err
	}
	src, err := c.ProductDeliverySource.Get(ctx, it.DeliverySourceID)
	if err != nil {
		return err
	}
	if src.ProductID != it.ProductID || src.SkuID != it.SkuID || src.SubsiteID != o.SubsiteID {
		return fmt.Errorf("共享交付范围不匹配")
	}
	if src.Status != "ready" || len(src.Content) == 0 || src.ExpiresAt > 0 && src.ExpiresAt <= time.Now().Unix() {
		if src.Status == "paused" || src.Status == "failed" || src.Status == "uncertain" || src.ExpiresAt > 0 && src.ExpiresAt <= time.Now().Unix() {
			return c.OrderItem.UpdateOneID(it.ID).SetFulfillmentStatus("failed").Exec(ctx)
		}
		return nil
	}
	processing, err := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusEQ(refundorder.StatusProcessing)).Exist(ctx)
	if err != nil || processing {
		return err
	}
	if _, err := r.cipher.Open(src.Content, it.ProductID, o.SubsiteID); err != nil {
		return fmt.Errorf("共享内容不可解密，请联系商家处理")
	}
	// A late payment may arrive after its reservation was released. Atomically
	// cap actual deliveries as well as admissions, including across workers.
	n, err := c.ProductDeliverySource.Update().Where(productdeliverysource.ID(src.ID), productdeliverysource.Status("ready"), productdeliverysource.Or(productdeliverysource.ExpiresAt(0), productdeliverysource.ExpiresAtGT(time.Now().Unix())), func(sel *entsql.Selector) {
		sel.Where(entsql.Or(entsql.EQ(sel.C(productdeliverysource.FieldMaxDeliveries), 0), entsql.ColumnsLT(sel.C(productdeliverysource.FieldDeliveredCount), sel.C(productdeliverysource.FieldMaxDeliveries))))
	}).AddDeliveredCount(1).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return c.OrderItem.UpdateOneID(it.ID).SetFulfillmentStatus("failed").Exec(ctx)
	}
	_, err = c.OrderDelivery.Create().SetOrderID(o.ID).SetItemID(it.ID).SetCardID(0).SetDeliveredQuantity(it.Quantity).SetDeliveryTokenHash(hashToken(randomToken())).SetDeliveredMode(orderdelivery.DeliveredModeDirect).SetDeliveredAt(time.Now().UTC()).Save(ctx)
	return err
}
