package order

// 购物车（ 销账）：登录态 CRUD + 商品快照联查。
// 结算复用 CreateOrder（前端组装 items/coupon/control_answers）——本文件无下单逻辑。
// 语义：同 (user, product, sku) 合并数量（唯一索引 upsert）；下架/隐藏商品 valid=false 打标。

import (
	"context"
	"errors"

	"entgo.io/ent/dialect/sql"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cartitem"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreCartService 购物车服务。
type StoreCartService struct {
	storefrontv1.UnimplementedStoreCartServiceServer
	data    *data.Data
	pricing catalogport.PricingResolver
	inv     invport.Inventory // 库存快照（Stock 单查；批量留优化）
}

// NewStoreCartService 构造（wire；Pricing/Inventory 复用 order 依赖注入链）。
func NewStoreCartService(d *data.Data, pricing catalogport.PricingResolver, inv invport.Inventory) *StoreCartService {
	return &StoreCartService{data: d, pricing: pricing, inv: inv}
}

func mustUserClaims(ctx context.Context) (uint64, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return 0, errors.New("identity.UNAUTHORIZED")
	}
	return claims.Subject, nil
}

// AddCartItem 加购（合并语义；商品须存在，价格/库存快照在列表联查）。
func (s *StoreCartService) AddCartItem(ctx context.Context, req *storefrontv1.AddCartItemRequest) (*storefrontv1.CartItem, error) {
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetQuantity() <= 0 || req.GetQuantity() > 99 {
		return nil, errors.New("cart.QUANTITY_INVALID: 1-99")
	}
	client := data.Client(ctx, s.data)
	// 商品存在性（上架校验留给列表打标——允许下架商品留在车内可见）
	if _, err := client.Product.Get(ctx, req.GetProductId()); err != nil {
		return nil, errors.New("cart.PRODUCT_NOT_FOUND")
	}
	// 合并：唯一索引 (user_id, product_id, sku_id) 冲突时数量累加（AddQuantity）
	if err := client.CartItem.Create().
		SetUserID(userID).
		SetProductID(req.GetProductId()).
		SetSkuID(req.GetSkuId()).
		SetQuantity(req.GetQuantity()).
		OnConflict(
			sql.ConflictColumns(cartitem.FieldUserID, cartitem.FieldProductID, cartitem.FieldSkuID),
		).Update(func(u *ent.CartItemUpsert) {
		u.AddQuantity(req.GetQuantity())
	}).Exec(ctx); err != nil {
		return nil, err
	}
	// 回读合并后的行（upsert 无 returning）
	row, err := s.myItem(ctx, userID, req.GetProductId(), req.GetSkuId())
	if err != nil {
		return nil, err
	}
	return s.toItemPB(ctx, row)
}

// ListCart 我的购物车（快照联查 + 失效打标）。
func (s *StoreCartService) ListCart(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.ListCartReply, error) {
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := data.Client(ctx, s.data).CartItem.Query().
		Where(cartitem.UserID(userID)).
		Order(ent.Desc(cartitem.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListCartReply{}
	for _, row := range rows {
		item, err := s.toItemPB(ctx, row)
		if err != nil {
			return nil, err
		}
		reply.Items = append(reply.Items, item)
		if item.GetValid() {
			reply.Total++
		}
	}
	return reply, nil
}

// UpdateCartItem 改量（0=删除；属主校验）。
func (s *StoreCartService) UpdateCartItem(ctx context.Context, req *storefrontv1.UpdateCartItemRequest) (*storefrontv1.CartItem, error) {
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.myItemByID(ctx, userID, req.GetId())
	if err != nil {
		return nil, errors.New("cart.ITEM_NOT_FOUND")
	}
	if req.GetQuantity() < 0 || req.GetQuantity() > 99 {
		return nil, errors.New("cart.QUANTITY_INVALID: 0-99")
	}
	client := data.Client(ctx, s.data)
	if req.GetQuantity() == 0 {
		if err := client.CartItem.DeleteOneID(row.ID).Exec(ctx); err != nil {
			return nil, err
		}
		return &storefrontv1.CartItem{Id: row.ID}, nil
	}
	if err := client.CartItem.UpdateOne(row).SetQuantity(req.GetQuantity()).Exec(ctx); err != nil {
		return nil, err
	}
	row.Quantity = req.GetQuantity()
	return s.toItemPB(ctx, row)
}

// RemoveCartItem 移除（属主校验）。
func (s *StoreCartService) RemoveCartItem(ctx context.Context, req *storefrontv1.RemoveCartItemRequest) (*emptypb.Empty, error) {
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.myItemByID(ctx, userID, req.GetId())
	if err != nil {
		return nil, errors.New("cart.ITEM_NOT_FOUND")
	}
	if err := data.Client(ctx, s.data).CartItem.DeleteOne(row).Exec(ctx); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ── 内部 ────────────────────────────────────────────────────────

func (s *StoreCartService) myItem(ctx context.Context, userID, productID, skuID uint64) (*ent.CartItem, error) {
	return data.Client(ctx, s.data).CartItem.Query().
		Where(
			cartitem.UserID(userID),
			cartitem.ProductID(productID),
			cartitem.SkuID(skuID),
		).Only(ctx)
}

func (s *StoreCartService) myItemByID(ctx context.Context, userID, id uint64) (*ent.CartItem, error) {
	return data.Client(ctx, s.data).CartItem.Query().
		Where(cartitem.UserID(userID), cartitem.ID(id)).Only(ctx)
}

// toItemPB 行 → PB（联查商品快照；商品缺失/下架 → valid=false）。
func (s *StoreCartService) toItemPB(ctx context.Context, row *ent.CartItem) (*storefrontv1.CartItem, error) {
	client := data.Client(ctx, s.data)
	p, err := client.Product.Get(ctx, row.ProductID)
	item := &storefrontv1.CartItem{
		Id: row.ID, ProductId: row.ProductID, SkuId: row.SkuID,
		Quantity: row.Quantity,
		AddedAt:  row.CreatedAt.Unix(),
	}
	if ent.IsNotFound(err) {
		item.Valid = false // 商品已删
		return item, nil
	}
	if err != nil {
		return nil, err
	}
	item.ProductName = p.Name
	item.ProductCover = p.Cover
	item.PointsRequired = p.PointsRequired
	item.PointsOnly = p.PointsRequired > 0
	item.Valid = p.Status == 1 // 上架 on_sale（隐藏/下架打标失效） // 上架才可选（隐藏/下架打标）
	// 现价（SKU 解析；失败回落商品价）
	var price money.Cents = money.Cents(p.Price)
	if row.SkuID > 0 && s.pricing != nil {
		if sp, err := s.pricing.ResolvePrice(ctx, row.ProductID, row.SkuID); err == nil {
			price = sp
		}
	}
	item.PriceCents = int64(price)
	// 库存（inventory port 单查；失败降级 0——宁显无货不显假库存）
	if p.StockType == "card" && s.inv != nil {
		if stock, err := s.inv.Stock(ctx, row.ProductID, row.SkuID); err == nil {
			item.Stock = stock
		}
	} else {
		item.Stock = -1 // 链接/兑换码类不限
	}
	return item, nil
}
