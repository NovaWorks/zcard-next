package catalog

import (
	"context"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cartitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryactivity"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/review"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyorder"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// 已删除商品保留归档行，供历史取货解密、订单商品信息及上游同步防复活使用。
const deletedProductStatus int8 = -1

func (s *AdminCatalogService) PreviewDeleteProduct(ctx context.Context, req *adminv1.GetProductRequest) (*adminv1.DeleteProductPreview, error) {
	c := data.Client(ctx, s.repo.data)
	p, err := c.Product.Query().Where(product.ID(req.Id), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.PRODUCT_NOT_FOUND", "商品不存在或已删除")
	}
	if err != nil {
		return nil, err
	}
	reply, _, err := inspectProductDeletion(ctx, c, p)
	return reply, err
}

func inspectProductDeletion(ctx context.Context, c *ent.Client, p *ent.Product) (*adminv1.DeleteProductPreview, []uint64, error) {
	reply := &adminv1.DeleteProductPreview{Name: p.Name}
	if p.Status != 0 {
		reply.DeleteBlockReason = "请先将商品下架，再进行删除"
	}
	rows, err := c.Order.Query().Where(order.HasItemsWith(orderitem.ProductID(p.ID))).WithItems().All(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]uint64, 0, len(rows))
	itemIDs := []uint64{}
	reply.OrderCount = int64(len(rows))
	for _, o := range rows {
		ids = append(ids, o.ID)
		if o.SubsiteID != p.SubsiteID {
			reply.DeleteBlockReason = "该商品存在其他站点的关联订单，请先处理关联关系"
		}
		switch o.Status {
		case order.StatusDelivered, order.StatusCompleted, order.StatusCanceled, order.StatusExpired, order.StatusRefunded:
		default:
			reply.DeleteBlockReason = "存在待付款、发货中或退款中的订单，请先完成、取消或退款后再删除"
		}
		for _, it := range o.Edges.Items {
			itemIDs = append(itemIDs, it.ID)
			if it.ProductID != p.ID {
				reply.DeleteOrdersBlockReason = "关联订单包含其他商品，只能删除商品并保留订单，避免误删其他商品记录"
			}
		}
		if o.ParentID != 0 {
			reply.DeleteOrdersBlockReason = "关联订单属于父子订单，只能删除商品并保留订单"
		}
	}
	n, err := c.Card.Query().Where(card.ProductID(p.ID), card.StatusIn(card.StatusAvailable, card.StatusReserved)).Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	if n > 0 {
		reply.DeleteBlockReason = fmt.Sprintf("还有 %d 张未售或锁定卡密，请先导出并清理库存或处理占用订单", n)
	}
	// 分批检查，避免大目录超过数据库绑定参数上限。
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		n, err = c.RefundOrder.Query().Where(refundorder.OrderIDIn(chunk...), refundorder.StatusIn(refundorder.StatusCreated, refundorder.StatusProcessing)).Count(ctx)
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			reply.DeleteBlockReason = "存在尚未完成的退款，请处理后再删除"
		}
		n, err = c.AffiliateCommission.Query().Where(affiliatecommission.OrderIDIn(chunk...), affiliatecommission.StatusEQ(affiliatecommission.StatusPendingConfirm)).Count(ctx)
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			reply.DeleteOrdersBlockReason = "关联订单有待结算佣金，请结算或撤销后再删除订单"
		}
		n, err = c.SupplyOrder.Query().Where(supplyorder.LocalOrderIDIn(chunk...)).Count(ctx)
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			reply.DeleteOrdersBlockReason = "关联订单用于下游供货，只能删除商品并保留供货订单"
		}
		n, err = c.Order.Query().Where(order.ParentIDIn(chunk...)).Count(ctx)
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			reply.DeleteOrdersBlockReason = "关联订单包含子订单，只能删除商品并保留订单"
		}
	}
	for start := 0; start < len(itemIDs); start += 200 {
		end := start + 200
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		n, err = c.ProcurementOrder.Query().Where(procurementorder.OrderItemIDIn(itemIDs[start:end]...), procurementorder.StatusNotIn(procurementorder.StatusFulfilled, procurementorder.StatusRejected, procurementorder.StatusRefunded)).Count(ctx)
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			reply.DeleteBlockReason = "存在尚未处理完成的上游采购，请处理后再删除"
		}
	}
	activityIDs, err := c.LotteryActivity.Query().Where(lotteryactivity.SubsiteID(p.SubsiteID), lotteryactivity.Published(true), lotteryactivity.StatusNotIn("ended", "archived")).IDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	linked, err := c.LotteryPrize.Query().Where(lotteryprize.ProductID(p.ID), lotteryprize.Enabled(true), lotteryprize.ActivityIDIn(activityIDs...)).Exist(ctx)
	if err != nil {
		return nil, nil, err
	}
	if linked {
		reply.DeleteBlockReason = "商品用于已发布的抽奖活动，请先结束或归档活动"
	}
	awarded, err := c.LotteryDraw.Query().Where(lotterydraw.ProductID(p.ID)).Exist(ctx)
	if err != nil {
		return nil, nil, err
	}
	if awarded {
		reply.DeleteOrdersBlockReason = "商品已有抽奖发放记录，只能删除商品并保留历史数据"
	}
	if reply.DeleteBlockReason != "" {
		reply.DeleteOrdersBlockReason = reply.DeleteBlockReason
	}
	return reply, ids, nil
}

func (s *AdminCatalogService) DeleteProduct(ctx context.Context, req *adminv1.DeleteProductRequest) (*emptypb.Empty, error) {
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		tenant := tenancy.FromContext(ctx).SubsiteID
		// 条件写取得商品行锁；下架/同步/删除串行，事务失败不留下半删除状态。
		n, err := c.Product.Update().Where(product.ID(req.Id), product.SubsiteID(tenant), product.Status(0)).AddSort(0).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.BadRequest("catalog.DELETE_REQUIRES_OFFSHELF", "商品不存在、已删除或未下架，请刷新后重试")
		}
		// 锁住关联订单，再重新读取其状态，阻止支付/退款在检查后更改订单。
		_, err = c.Order.Update().Where(order.HasItemsWith(orderitem.ProductID(req.Id))).AddVersion(0).Save(ctx)
		if err != nil {
			return err
		}
		p, err := c.Product.Get(ctx, req.Id)
		if err != nil {
			return err
		}
		preview, ids, err := inspectProductDeletion(ctx, c, p)
		if err != nil {
			return err
		}
		reason := preview.DeleteBlockReason
		if req.DeleteOrders {
			reason = preview.DeleteOrdersBlockReason
		}
		if reason != "" {
			return errors.BadRequest("catalog.DELETE_BLOCKED", reason)
		}
		if req.DeleteOrders {
			if req.ConfirmName != p.Name || req.ExpectedOrderCount != preview.OrderCount {
				return errors.BadRequest("catalog.DELETE_CONFIRMATION_CHANGED", "请重新确认商品名称及关联订单数量后再删除")
			}
			if err := purgeProductOrders(ctx, c, ids); err != nil {
				return err
			}
			if _, err := c.Card.Delete().Where(card.ProductID(p.ID)).Exec(ctx); err != nil {
				return err
			}
			if err := c.Product.UpdateOneID(p.ID).ClearDirectContent().Exec(ctx); err != nil {
				return err
			}
		}
		if _, err = c.CartItem.Delete().Where(cartitem.ProductID(p.ID)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.SupplyMapping.Delete().Where(supplymapping.LocalProductID(p.ID)).Exec(ctx); err != nil {
			return err
		}
		// 仅删除商品时保留历史卡密、封面与直发密文，历史订单可继续查询和取货。
		return c.Product.UpdateOneID(p.ID).SetStatus(deletedProductStatus).SetIsRecommend(false).ClearCategoryID().Exec(ctx)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// 清除订单业务数据。保留支付幂等锚点及钱包/积分/佣金账务流水，不更改余额。
func purgeProductOrders(ctx context.Context, c *ent.Client, ids []uint64) error {
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		items, err := c.OrderItem.Query().Where(orderitem.OrderIDIn(batch...)).IDs(ctx)
		if err != nil {
			return err
		}
		// 商品相同也可能有多个规格子项，继续分批处理采购记录。
		for i := 0; i < len(items); i += 200 {
			j := i + 200
			if j > len(items) {
				j = len(items)
			}
			procs, err := c.ProcurementOrder.Query().Where(procurementorder.OrderItemIDIn(items[i:j]...)).IDs(ctx)
			if err != nil {
				return err
			}
			for _, id := range procs {
				if _, err = c.ProcurementItem.Delete().Where(procurementitem.ProcurementID(id)).Exec(ctx); err != nil {
					return err
				}
			}
			if _, err = c.ProcurementOrder.Delete().Where(procurementorder.OrderItemIDIn(items[i:j]...)).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err = c.OrderDelivery.Delete().Where(orderdelivery.OrderIDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.RefundOrder.Delete().Where(refundorder.OrderIDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.Review.Delete().Where(review.OrderIDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.Payment.Update().Where(payment.OrderIDIn(batch...)).ClearOrderID().Save(ctx); err != nil {
			return err
		}
		if _, err = c.OrderAmountLine.Delete().Where(orderamountline.OrderIDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.OrderStatusEvent.Delete().Where(orderstatusevent.OrderIDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.OrderItem.Delete().Where(orderitem.OrderIDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.Order.Delete().Where(order.IDIn(batch...)).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
