package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cartitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/categoryproductplacement"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplierproductprice"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Keep the historical anchor for delivery decryption, outstanding fulfillment
// and import tombstones. It is absent from every live catalog.
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
	n, err := c.Order.Query().Where(order.HasItemsWith(orderitem.ProductID(p.ID))).Count(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.DeleteProductPreview{Name: p.Name, OrderCount: int64(n), DeleteOrdersBlockReason: "删除商品时保留订单快照、交付及退款记录"}, nil
}

func (s *AdminCatalogService) DeleteProduct(ctx context.Context, req *adminv1.DeleteProductRequest) (*emptypb.Empty, error) {
	// Old clients cannot accidentally purge history through the former option.
	if req.DeleteOrders {
		return nil, errors.BadRequest("catalog.ORDER_HISTORY_REQUIRED", "删除商品时必须保留订单快照，不能同时删除订单")
	}
	if err := s.repo.DeleteProduct(ctx, req.Id); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// archiveProduct runs in the caller's transaction, including category cascades.
// Explicit deletion allows locked products and outstanding orders. Historical
// rows remain usable by existing deliveries, compensation and refunds.
func (r *ProductRepoImpl) archiveProduct(ctx context.Context, id uint64) error {
	c := data.Client(ctx, r.data)
	tenant := tenancy.FromContext(ctx).SubsiteID
	if r.data.Dialect == db.SQLite {
		if err := c.Product.Update().Where(product.ID(id), product.SubsiteID(tenant)).AddLockVersion(0).Exec(ctx); err != nil {
			return err
		}
	}
	q := c.Product.Query().Where(product.ID(id), product.SubsiteID(tenant), product.StatusGTE(0))
	if r.data.Dialect != db.SQLite {
		q.ForUpdate()
	}
	p, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return errors.NotFound("catalog.PRODUCT_NOT_FOUND", "商品不存在或已删除")
	}
	if err != nil {
		return err
	}
	if err := c.OrderItem.Update().Where(orderitem.ProductID(id), orderitem.ProductName("")).SetProductName(p.Name).Exec(ctx); err != nil {
		return err
	}
	orders, err := c.Order.Query().Where(order.HasItemsWith(orderitem.ProductID(id))).All(ctx)
	if err != nil {
		return err
	}
	unfinished := false
	for _, o := range orders {
		switch o.Status {
		case order.StatusDelivered, order.StatusCompleted, order.StatusCanceled, order.StatusExpired, order.StatusRefunded:
		default:
			unfinished = true
		}
	}
	// Reserved cards and stock needed by previously admitted orders stay intact.
	if !unfinished {
		if err = c.Card.Update().Where(card.ProductID(id), card.StatusEQ(card.StatusAvailable)).SetStatus(card.StatusDisabled).Exec(ctx); err != nil {
			return err
		}
	}
	if _, err = c.CartItem.Delete().Where(cartitem.ProductID(id)).Exec(ctx); err != nil {
		return err
	}
	if _, err = c.SupplyMapping.Delete().Where(supplymapping.LocalProductID(id)).Exec(ctx); err != nil {
		return err
	}
	if _, err = c.CategoryProductPlacement.Delete().Where(categoryproductplacement.ProductID(id)).Exec(ctx); err != nil {
		return err
	}
	if _, err = c.SupplierProductPrice.Delete().Where(supplierproductprice.ProductID(id), supplierproductprice.Scope("product")).Exec(ctx); err != nil {
		return err
	}
	// Published lotteries must stop awarding a deleted product.
	if err = c.LotteryPrize.Update().Where(lotteryprize.ProductID(id)).SetEnabled(false).Exec(ctx); err != nil {
		return err
	}
	if len(orders) == 0 {
		if err = data.SyncProductMediaRefs(ctx, r.data, p, nil); err != nil {
			return err
		}
	}
	return c.Product.UpdateOneID(id).SetStatus(deletedProductStatus).SetIsRecommend(false).SetAutoListing(false).SetListingRestocked(false).SetIsLocked(false).ClearCategoryID().AddLockVersion(1).Exec(ctx)
}
