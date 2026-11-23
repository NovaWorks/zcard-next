package order

// 购物车（ 销账）：登录态 CRUD + 商品快照联查。
// 结算复用 CreateOrder（前端组装 items/coupon/control_answers）——本文件无下单逻辑。
// 语义：同 (user, product, sku) 合并数量（唯一索引 upsert）；下架/隐藏商品 valid=false 打标。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	couponport "github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/structpb"

	"entgo.io/ent/dialect/sql"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cartitem"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreCartService 购物车服务。
type StoreCartService struct {
	storefrontv1.UnimplementedStoreCartServiceServer
	settings  settingsport.Provider
	flash     couponport.FlashResolver
	data      *data.Data
	pricing   catalogport.PricingResolver
	inv       invport.Inventory // 库存快照（Stock 单查；批量留优化）
	discounts orderport.DiscountResolver
}

// NewStoreCartService 构造（wire；Pricing/Inventory 复用 order 依赖注入链）。
func NewStoreCartService(d *data.Data, pricing catalogport.PricingResolver, inv invport.Inventory, settings settingsport.Provider, flash couponport.FlashResolver, discounts orderport.DiscountResolver) *StoreCartService {
	return &StoreCartService{data: d, pricing: pricing, inv: inv, settings: settings, flash: flash, discounts: discounts}
}

func mustUserClaims(ctx context.Context) (uint64, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return 0, kerrors.Unauthorized("identity.UNAUTHORIZED", "登录状态已失效，请重新登录")
	}
	return claims.Subject, nil
}

// AddCartItem 加购（合并语义；商品须存在，价格/库存快照在列表联查）。
func (s *StoreCartService) AddCartItem(ctx context.Context, req *storefrontv1.AddCartItemRequest) (*storefrontv1.CartItem, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetQuantity() <= 0 {
		return nil, errors.New("cart.QUANTITY_INVALID: 1-99")
	}
	client := data.Client(ctx, s.data)
	// 商品存在性（上架校验留给列表打标——允许下架商品留在车内可见）
	p, err := client.Product.Get(ctx, req.GetProductId())
	if err != nil {
		return nil, errors.New("cart.PRODUCT_NOT_FOUND")
	}
	if p.DeliveryKind != "card" {
		return nil, errors.New("接码商品须独立单件购买，不能加入购物车")
	}
	hidden, err := data.HiddenCategoryIDs(ctx, client, p.SubsiteID)
	if err != nil {
		return nil, err
	}
	if data.CategoryHidden(hidden, p.CategoryID) {
		return nil, errors.New("cart.PRODUCT_NOT_AVAILABLE: 商品已下架")
	}
	sku, err := data.DeliverySKU(ctx, client, p, req.GetSkuId())
	if err != nil {
		return nil, errors.New("cart.SKU_INVALID")
	}
	reuse := data.StockSource(p, sku) == "reuse"
	if reuse && req.GetQuantity() != 1 {
		return nil, errors.New("该规格每次只需购买一份")
	}
	err = data.PurchaseRetry(ctx, s.data, func(txCtx context.Context) error {
		if e := data.LockPurchaseProducts(txCtx, s.data, p.SubsiteID, []uint64{p.ID}); e != nil {
			return e
		}
		current, e := data.Client(txCtx, s.data).Product.Get(txCtx, p.ID)
		if e != nil {
			return e
		}
		quantity := int64(req.GetQuantity())
		old, e := s.myItem(txCtx, userID, p.ID, req.GetSkuId())
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		if old != nil && !reuse {
			quantity += int64(old.Quantity)
		}
		if reuse {
			quantity = 1
		}
		if quantity > 2147483647 {
			return fmt.Errorf("cart.QUANTITY_INVALID")
		}
		if e := cartCardQuantity(current, int(quantity)); e != nil {
			return e
		}
		// 合并：唯一索引 (user_id, product_id, sku_id) 冲突时数量累加（AddQuantity）
		if err := data.Client(txCtx, s.data).CartItem.Create().
			SetUserID(userID).
			SetProductID(req.GetProductId()).
			SetSkuID(req.GetSkuId()).
			SetQuantity(req.GetQuantity()).
			OnConflict(
				sql.ConflictColumns(cartitem.FieldUserID, cartitem.FieldProductID, cartitem.FieldSkuID),
			).Update(func(u *ent.CartItemUpsert) {
			if reuse {
				u.SetQuantity(1)
			} else {
				u.AddQuantity(req.GetQuantity())
			}
		}).Exec(txCtx); err != nil {
			return err
		}
		return nil

	})
	if err != nil {
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
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
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
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.myItemByID(ctx, userID, req.GetId())
	if err != nil {
		return nil, errors.New("cart.ITEM_NOT_FOUND")
	}
	if req.GetQuantity() < 0 {
		return nil, errors.New("cart.QUANTITY_INVALID: 0-99")
	}
	if req.GetQuantity() == 0 {
		if _, e := s.RemoveCartItem(ctx, &storefrontv1.RemoveCartItemRequest{Id: row.ID}); e != nil {
			return nil, e
		}
		return &storefrontv1.CartItem{Id: row.ID}, nil
	}
	anchor, err := data.Client(ctx, s.data).Product.Get(ctx, row.ProductID)
	if err != nil {
		return nil, err
	}
	err = data.PurchaseRetry(ctx, s.data, func(tx context.Context) error {
		c := data.Client(tx, s.data)
		if e := data.LockPurchaseProducts(tx, s.data, anchor.SubsiteID, []uint64{anchor.ID}); e != nil {
			return e
		}
		current, e := s.myItemByID(tx, userID, req.GetId())
		if e != nil {
			return e
		}
		if req.GetQuantity() == 0 {
			return c.CartItem.DeleteOneID(current.ID).Exec(tx)
		}
		p, e := c.Product.Get(tx, anchor.ID)
		if e != nil {
			return e
		}
		if e = cartCardQuantity(p, int(req.GetQuantity())); e != nil {
			return e
		}
		sku, e := data.DeliverySKU(tx, c, p, current.SkuID)
		if e != nil {
			return e
		}
		if data.StockSource(p, sku) == "reuse" && req.GetQuantity() != 1 {
			return fmt.Errorf("该规格每次只需购买一份")
		}
		return c.CartItem.UpdateOneID(current.ID).SetQuantity(req.GetQuantity()).Exec(tx)
	})
	if err != nil {
		return nil, err
	}
	if req.GetQuantity() == 0 {
		return &storefrontv1.CartItem{Id: row.ID}, nil
	}
	row.Quantity = req.GetQuantity()
	return s.toItemPB(ctx, row)
}

// RemoveCartItem 移除（属主校验）。
func (s *StoreCartService) RemoveCartItem(ctx context.Context, req *storefrontv1.RemoveCartItemRequest) (*emptypb.Empty, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	userID, err := mustUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.myItemByID(ctx, userID, req.GetId())
	if err != nil {
		return nil, errors.New("cart.ITEM_NOT_FOUND")
	}
	anchor, e := data.Client(ctx, s.data).Product.Get(ctx, row.ProductID)
	if ent.IsNotFound(e) {
		if e = data.Client(ctx, s.data).CartItem.DeleteOneID(row.ID).Exec(ctx); e != nil {
			return nil, e
		}
		return &emptypb.Empty{}, nil
	}
	if e != nil {
		return nil, e
	}
	if e = data.PurchaseRetry(ctx, s.data, func(tx context.Context) error {
		if e := data.LockPurchaseProducts(tx, s.data, anchor.SubsiteID, []uint64{anchor.ID}); e != nil {
			return e
		}
		current, e := s.myItemByID(tx, userID, row.ID)
		if e != nil {
			return e
		}
		return data.Client(tx, s.data).CartItem.DeleteOneID(current.ID).Exec(tx)
	}); e != nil {
		return nil, e
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
	rules, ruleErr := cardpurchase.Decode(p.ControlConfig[cardpurchase.ConfigKey])
	if ruleErr != nil {
		return nil, ruleErr
	}
	if rules != nil {
		raw, _ := json.Marshal(rules)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		item.CardPurchase, _ = structpb.NewStruct(v)
		item.MaxQuantity = rules.Maximum
	}
	item.ProductName = p.Name
	item.ProductCover = p.Cover
	item.PointsRequired = p.PointsRequired
	item.PointsOnly = p.PointsRequired > 0
	if p.DeliveryKind != "card" {
		return nil, errors.New("接码商品须独立单件购买，不能加入购物车")
	}
	hidden, err := data.HiddenCategoryIDs(ctx, client, p.SubsiteID)
	if err != nil {
		return nil, err
	}
	item.Valid = p.Status == 1 && !data.CategoryHidden(hidden, p.CategoryID) // 上架 on_sale（隐藏/下架打标失效） // 上架才可选（隐藏/下架打标）
	// 现价（SKU 解析；失败回落商品价）
	var price money.Cents = money.Cents(p.Price)
	if row.SkuID > 0 && s.pricing != nil {
		if sp, err := s.pricing.ResolvePrice(ctx, row.ProductID, row.SkuID); err == nil {
			price = sp
		}
	}
	if s.discounts != nil {
		price, err = s.discounts.DiscountedPrice(ctx, row.ProductID, price)
		if err != nil {
			return nil, err
		}
	}
	item.PriceCents = int64(price)
	if s.flash != nil {
		f, err := s.flash.Active(ctx, row.ProductID, row.SkuID)
		if err != nil {
			return nil, err
		}
		if f != nil {
			item.FlashSale = &storefrontv1.FlashOffer{PriceCents: int64(f.FlashPrice), EndAt: f.EndAt.Unix(), Remaining: f.Remaining, PerUserLimit: f.PerUserLimit}
		}
	}
	// 库存（inventory port 单查；失败降级 0——宁显无货不显假库存）
	sku, skuErr := data.DeliverySKU(ctx, client, p, row.SkuID)
	if skuErr != nil {
		item.Valid = false
		item.Stock = 0
		return item, nil
	}
	if data.StockSource(p, sku) == "reuse" {
		item.MaxQuantity = 1
	}
	if data.StockSource(p, sku) != "upstream" {
		stock, e := data.LocalSKUStock(ctx, s.data, p, sku)
		if e != nil {
			return nil, e
		}
		item.Stock = stock
	} else if p.UpstreamSourceID > 0 {
		item.Stock = -2
		if stocks, err := data.ProductStocks(ctx, s.data, []*ent.Product{p}); err == nil {
			item.Stock = stocks[p.ID]
		}
	} else if s.inv != nil {
		if stock, err := s.inv.Stock(ctx, row.ProductID, row.SkuID); err == nil {
			item.Stock = stock
		}
	} else {
		item.Stock = -1 // 链接/兑换码类不限
	}
	return item, nil
}

// 运行时读取开关；关闭只阻止操作，不删除已有购物车。
func (s *StoreCartService) requireEnabled(ctx context.Context) error {
	if s.settings == nil {
		return kerrors.ServiceUnavailable("cart.CONFIG_UNAVAILABLE", "购物车配置暂时无法读取")
	}
	raw, err := s.settings.GetDefault(ctx, "trade", "cart_enabled", json.RawMessage(`true`))
	if err != nil {
		return kerrors.ServiceUnavailable("cart.CONFIG_UNAVAILABLE", "购物车配置暂时无法读取")
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil {
		return kerrors.ServiceUnavailable("cart.CONFIG_INVALID", "购物车配置无效")
	}
	if !enabled {
		return kerrors.Forbidden("cart.DISABLED", "购物车已关闭，请在商品详情页直接购买")
	}
	return nil
}

func cartCardQuantity(p *ent.Product, quantity int) error {
	rules, e := cardpurchase.Decode(p.ControlConfig[cardpurchase.ConfigKey])
	if e != nil {
		return e
	}
	if rules == nil && quantity > 99 {
		return fmt.Errorf("cart.QUANTITY_INVALID: 1-99")
	}
	return rules.Validate(quantity)
}
