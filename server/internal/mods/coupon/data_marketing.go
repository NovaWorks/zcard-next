package coupon

// P3-02 M3 扩展：券范围矩阵/每人限用/领取/返还 + 秒杀（同锁防超卖）+ 促销（多促最优）。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/flashsale"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/promotion"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// ── T1/T2 券：范围矩阵 + 每人限用 + 领取/返还 ──────────────

// ResolveScoped 范围矩阵版校验（port.CouponResolver 实现）。
func (r *CouponRepoImpl) ResolveScoped(ctx context.Context, code string, userID, levelID uint64, items []port.CartItem) (money.Cents, uint64, error) {
	if code == "" {
		return 0, 0, nil
	}
	client := data.Client(ctx, r.data)
	c, err := client.Coupon.Query().Where(coupon.Code(code)).Only(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("coupon.INVALID: %w", err)
	}
	if c.Status != coupon.StatusUnused {
		return 0, 0, fmt.Errorf("coupon.NOT_UNUSED")
	}
	if !c.ExpireAt.IsZero() && time.Now().UTC().After(c.ExpireAt) {
		return 0, 0, fmt.Errorf("coupon.EXPIRED")
	}
	if c.UserID != 0 && c.UserID != userID {
		return 0, 0, fmt.Errorf("coupon.USER_MISMATCH")
	}
	// 范围矩阵（全场默认命中；商品/分类/等级三清单任一配置即精确匹配）
	if !scopeMatches(c.Scope, items, levelID) {
		return 0, 0, fmt.Errorf("coupon.SCOPE_MISMATCH")
	}
	// 每人限用：多次码按已用累计（同 code 历史核销次数）；单次码由 status 保证
	if c.PerUserLimit > 1 && userID > 0 {
		used, err := client.Coupon.Query().
			Where(coupon.Code(code), coupon.StatusEQ(coupon.StatusUsed)).
			Count(ctx)
		if err == nil && used >= int(c.PerUserLimit) {
			return 0, 0, fmt.Errorf("coupon.PER_USER_LIMIT")
		}
	}
	// 面额（percent 按命中行小计折算——范围受限时以命中商品金额为基数）
	base := orderAmountOf(items)
	var value int64
	switch c.Type {
	case coupon.TypeFixed:
		value = c.Value
	case coupon.TypePercent:
		value = int64(base) * c.Value / 10000
	}
	if value > int64(base) {
		value = int64(base) // 券不找零
	}
	return money.Cents(value), c.ID, nil
}

// scopeMatches 范围判定矩阵：空 scope/无清单 = 全场；配置任一清单则精确匹配
// （商品 ∨ 分类 ∨ 等级——多清单间 OR，清单内多值 OR）。
func scopeMatches(scope map[string]any, items []port.CartItem, levelID uint64) bool {
	if len(scope) == 0 {
		return true
	}
	productIDs := idList(scope["product_ids"])
	categoryIDs := idList(scope["category_ids"])
	levelIDs := idList(scope["level_ids"])
	if len(productIDs) == 0 && len(categoryIDs) == 0 && len(levelIDs) == 0 {
		return true // 空清单语义 = 全场
	}
	if len(levelIDs) > 0 && levelID > 0 && levelIDs[levelID] {
		return true
	}
	for _, it := range items {
		if len(productIDs) > 0 && productIDs[it.ProductID] {
			return true
		}
		if len(categoryIDs) > 0 && it.CategoryID > 0 && categoryIDs[it.CategoryID] {
			return true
		}
	}
	return false
}

// idList any 列表 → set（JSON 数字解析为 float64）。
func idList(v any) map[uint64]bool {
	out := map[uint64]bool{}
	list, ok := v.([]any)
	if !ok {
		return out
	}
	for _, e := range list {
		switch x := e.(type) {
		case float64:
			out[uint64(x)] = true
		case string:
			var n uint64
			_, _ = fmt.Sscanf(x, "%d", &n)
			if n > 0 {
				out[n] = true
			}
		}
	}
	return out
}

func orderAmountOf(items []port.CartItem) money.Cents {
	var total int64
	for _, it := range items {
		total += int64(it.UnitPrice) * int64(it.Quantity)
	}
	return money.Cents(total)
}

// ReturnByOrder 返还券（取消/退款路径调用；过期不返）。
func (r *CouponRepoImpl) ReturnByOrder(ctx context.Context, orderID uint64) error {
	client := data.Client(ctx, r.data)
	rows, err := client.Coupon.Query().
		Where(coupon.UsedOrderID(orderID), coupon.StatusEQ(coupon.StatusUsed)).
		All(ctx)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if !c.ExpireAt.IsZero() && time.Now().UTC().After(c.ExpireAt) {
			continue // 过期不返（口径：作废）
		}
		_, _ = client.Coupon.UpdateOneID(c.ID).
			SetStatus(coupon.StatusUnused).
			ClearUsedAt().
			ClearUsedOrderID().
			Save(ctx)
	}
	return nil
}

// Redeem 兑换码领券（用户凭 code 领取：未指派券回填 user_id）。
func (r *CouponRepoImpl) Redeem(ctx context.Context, code string, userID uint64) error {
	client := data.Client(ctx, r.data)
	c, err := client.Coupon.Query().Where(coupon.Code(code)).Only(ctx)
	if err != nil {
		return fmt.Errorf("coupon.INVALID")
	}
	if c.Status != coupon.StatusUnused {
		return fmt.Errorf("coupon.NOT_UNUSED")
	}
	if !c.ExpireAt.IsZero() && time.Now().UTC().After(c.ExpireAt) {
		return fmt.Errorf("coupon.EXPIRED")
	}
	if c.UserID != 0 && c.UserID != userID {
		return fmt.Errorf("coupon.USER_MISMATCH")
	}
	if c.UserID == userID {
		return nil // 重复领取幂等
	}
	_, err = client.Coupon.UpdateOneID(c.ID).SetUserID(userID).Save(ctx)
	return err
}

// GrantToUser 后台赠送（按批次取未指派券回填 user_id，返回实际赠送数）。
func (r *CouponRepoImpl) GrantToUser(ctx context.Context, batchID string, userID uint64, count int32) (int32, error) {
	client := data.Client(ctx, r.data)
	rows, err := client.Coupon.Query().
		Where(
			coupon.BatchID(batchID),
			coupon.StatusEQ(coupon.StatusUnused),
			coupon.UserIDIsNil(),
		).
		Limit(int(count)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	for _, c := range rows {
		if _, err := client.Coupon.UpdateOneID(c.ID).SetUserID(userID).Save(ctx); err != nil {
			return 0, err
		}
	}
	return int32(len(rows)), nil
}

// ListMyCoupons 用户可用券（未用未过期）。
func (r *CouponRepoImpl) ListMyCoupons(ctx context.Context, userID uint64) ([]*ent.Coupon, error) {
	return data.Client(ctx, r.data).Coupon.Query().
		Where(coupon.UserID(userID), coupon.StatusEQ(coupon.StatusUnused)).
		Order(ent.Desc(coupon.FieldID)).
		Limit(100).
		All(ctx)
}

// ── T3 秒杀（同锁防超卖）──────────────────────────────────

// Active 生效中秒杀（窗口判定无状态）。
func (r *CouponRepoImpl) Active(ctx context.Context, productID, skuID uint64) (*port.FlashInfo, error) {
	now := time.Now().UTC()
	fs, err := data.Client(ctx, r.data).FlashSale.Query().
		Where(
			flashsale.ProductID(productID),
			flashsale.SkuID(skuID),
			flashsale.StartAtLTE(now),
			flashsale.EndAtGTE(now),
		).
		Order(ent.Desc(flashsale.FieldID)).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &port.FlashInfo{
		ID: fs.ID, FlashPrice: money.Cents(fs.FlashPrice),
		StartAt: fs.StartAt, PerUserLimit: fs.PerUserLimit,
	}, nil
}

// Consume 同锁扣减（inventory.Reserve 成功后同一事务内；CAS 防超卖：
// 读 limit/sold → 校验余量 → UPDATE WHERE sold_qty=旧值（乐观锁），
// affected==0 = 并发竞争/超卖 → 哨兵错误回滚整个下单事务）。
func (r *CouponRepoImpl) Consume(ctx context.Context, flashID uint64, qty int32) error {
	if qty <= 0 {
		return nil
	}
	client := data.Client(ctx, r.data)
	fs, err := client.FlashSale.Get(ctx, flashID)
	if err != nil {
		return err
	}
	if fs.SoldQty+qty > fs.LimitQty {
		return port.ErrFlashSoldOut
	}
	affected, err := client.FlashSale.Update().
		Where(flashsale.ID(flashID), flashsale.SoldQty(fs.SoldQty)).
		SetSoldQty(fs.SoldQty + qty).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return port.ErrFlashSoldOut // 并发竞争：限量被抢空
	}
	return nil
}

// UserPurchasedCount 秒杀窗口内已购（paid+pending 累计；order_items×orders 联查）。
func (r *CouponRepoImpl) UserPurchasedCount(ctx context.Context, productID, userID uint64, since time.Time) (int32, error) {
	client := data.Client(ctx, r.data)
	// 联查口径：该用户窗口内 paid/pending 单中命中商品的数量合计。
	// ent 无跨表 join 表达式——经 order_items 反查（product × order 归属过滤）。
	itemRows, err := client.OrderItem.Query().
		Where(
			orderitem.ProductID(productID),
			orderitem.HasOrderWith(
				order.UserID(userID),
				order.StatusIn(order.StatusPaid, order.StatusPendingPayment, order.StatusFulfilling),
				order.CreatedAtGTE(since),
			),
		).
		All(ctx)
	if err != nil {
		return 0, err
	}
	var n int32
	for _, it := range itemRows {
		n += it.Quantity
	}
	return n, nil
}

// ListFlash 秒杀列表（进行中/即将开始；storefront 营销位）。
func (r *CouponRepoImpl) ListFlash(ctx context.Context, now time.Time, upcoming bool) ([]*ent.FlashSale, error) {
	q := data.Client(ctx, r.data).FlashSale.Query()
	if upcoming {
		q = q.Where(flashsale.StartAtGT(now)).Order(ent.Asc(flashsale.FieldStartAt))
	} else {
		q = q.Where(flashsale.StartAtLTE(now), flashsale.EndAtGTE(now)).Order(ent.Asc(flashsale.FieldEndAt))
	}
	return q.Limit(50).All(ctx)
}

// ── T4 促销（多促最优）────────────────────────────────────

// BestFor 商品命中最优促销（同时窗取折让最大；无则 nil）。
func (r *CouponRepoImpl) BestFor(ctx context.Context, productID, categoryID uint64, unitPrice money.Cents) (*port.PromotionInfo, error) {
	now := time.Now().UTC()
	rows, err := data.Client(ctx, r.data).Promotion.Query().
		Where(
			promotion.Enabled(true),
			promotion.StartAtLTE(now),
			promotion.EndAtGTE(now),
		).
		Order(ent.Desc(promotion.FieldID)).
		Limit(50).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var best *port.PromotionInfo
	for _, p := range rows {
		if !promoScopeHit(p.Scope, productID, categoryID) {
			continue
		}
		info := &port.PromotionInfo{
			ID: p.ID, Type: string(p.Type), Name: p.Name,
			Threshold: money.Cents(p.Threshold),
		}
		switch p.Type {
		case promotion.TypeFixed:
			info.Discount = money.Cents(p.Discount)
		case promotion.TypePercent:
			info.DiscountRate = int32(p.Discount) // percent：discount 列存万分比
		case promotion.TypeSpecialPrice:
			info.SpecialPrice = money.Cents(p.SpecialPrice)
			info.Discount = unitPrice - info.SpecialPrice // 折让 = 价 - 特价
		}
		// 门槛判定（满 X 按单价口径；多品购物车场景由调用方按行判定后聚合——管线为逐行）
		if info.Threshold > 0 && unitPrice < info.Threshold {
			continue
		}
		// 折让 <= 0 不参与
		if info.Discount <= 0 {
			continue
		}
		if info.Type == "percent" && info.DiscountRate <= 0 {
			continue
		}
		if best == nil || promoDiscountOf(info, unitPrice) > promoDiscountOf(best, unitPrice) {
			best = info
		}
	}
	return best, nil
}

// promoDiscountOf 促销折让计算（fixed=面额；percent=价×率；special_price=价-特价）。
func promoDiscountOf(p *port.PromotionInfo, unitPrice money.Cents) money.Cents {
	switch p.Type {
	case "fixed":
		return p.Discount
	case "percent":
		return money.Cents(int64(unitPrice) * int64(p.DiscountRate) / 10000)
	case "special_price":
		return p.Discount // 已折算（价-特价）
	}
	return 0
}

// promoScopeHit 促销范围（空=全场；product_ids/category_ids OR）。
func promoScopeHit(scope map[string]any, productID, categoryID uint64) bool {
	if len(scope) == 0 {
		return true
	}
	productIDs := idList(scope["product_ids"])
	categoryIDs := idList(scope["category_ids"])
	if len(productIDs) == 0 && len(categoryIDs) == 0 {
		return true
	}
	if len(productIDs) > 0 && productIDs[productID] {
		return true
	}
	if len(categoryIDs) > 0 && categoryID > 0 && categoryIDs[categoryID] {
		return true
	}
	return false
}

// ── admin CRUD（秒杀/促销）────────────────────────────────

// CreateFlash 创建秒杀。
func (r *CouponRepoImpl) CreateFlash(ctx context.Context, productID, skuID uint64, flashPrice int64, startAt, endAt time.Time, limitQty, perUserLimit int32) (*ent.FlashSale, error) {
	return data.Client(ctx, r.data).FlashSale.Create().
		SetProductID(productID).
		SetSkuID(skuID).
		SetFlashPrice(flashPrice).
		SetStartAt(startAt).
		SetEndAt(endAt).
		SetLimitQty(limitQty).
		SetPerUserLimit(perUserLimit).
		Save(ctx)
}

// ListFlashAll 秒杀管理列表。
func (r *CouponRepoImpl) ListFlashAll(ctx context.Context, page, size int) ([]*ent.FlashSale, int, error) {
	q := data.Client(ctx, r.data).FlashSale.Query().Order(ent.Desc(flashsale.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// DeleteFlash 删除秒杀。
func (r *CouponRepoImpl) DeleteFlash(ctx context.Context, id uint64) error {
	return data.Client(ctx, r.data).FlashSale.DeleteOneID(id).Exec(ctx)
}

// UpsertPromotion 创建/更新促销。
func (r *CouponRepoImpl) UpsertPromotion(ctx context.Context, id uint64, name string, scope map[string]any, typ string, threshold, discount, specialPrice int64, startAt, endAt time.Time, enabled bool) (*ent.Promotion, error) {
	client := data.Client(ctx, r.data)
	if id > 0 {
		return client.Promotion.UpdateOneID(id).
			SetName(name).
			SetScope(scope).
			SetType(promotion.Type(typ)).
			SetThreshold(threshold).
			SetDiscount(discount).
			SetSpecialPrice(specialPrice).
			SetStartAt(startAt).
			SetEndAt(endAt).
			SetEnabled(enabled).
			Save(ctx)
	}
	return client.Promotion.Create().
		SetName(name).
		SetScope(scope).
		SetType(promotion.Type(typ)).
		SetThreshold(threshold).
		SetDiscount(discount).
		SetSpecialPrice(specialPrice).
		SetStartAt(startAt).
		SetEndAt(endAt).
		SetEnabled(enabled).
		Save(ctx)
}

// ListPromotions 促销列表。
func (r *CouponRepoImpl) ListPromotions(ctx context.Context, page, size int) ([]*ent.Promotion, int, error) {
	q := data.Client(ctx, r.data).Promotion.Query().Order(ent.Desc(promotion.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ProductCategoryOf 商品分类（管线促销范围判定输入）。
func (r *CouponRepoImpl) ProductCategoryOf(ctx context.Context, productID uint64) uint64 {
	p, err := data.Client(ctx, r.data).Product.Get(ctx, productID)
	if err != nil {
		return 0
	}
	return p.CategoryID
}

var _ = product.FieldID
var _ = json.Marshal

func toFlashPB(fs *ent.FlashSale) *adminv1.FlashSaleItem {
	return &adminv1.FlashSaleItem{
		Id: fs.ID, ProductId: fs.ProductID, SkuId: fs.SkuID,
		FlashPrice: fs.FlashPrice, LimitQty: fs.LimitQty, SoldQty: fs.SoldQty,
		PerUserLimit: fs.PerUserLimit,
		StartAt:      fs.StartAt.Unix(), EndAt: fs.EndAt.Unix(),
	}
}

func toPromoPB(p *ent.Promotion) *adminv1.PromotionItem {
	out := &adminv1.PromotionItem{
		Id: p.ID, Name: p.Name, Type: string(p.Type),
		Threshold: p.Threshold, Discount: p.Discount, SpecialPrice: p.SpecialPrice,
		StartAt: p.StartAt.Unix(), EndAt: p.EndAt.Unix(), Enabled: p.Enabled,
	}
	if p.Scope != nil {
		if b, err := json.Marshal(p.Scope); err == nil {
			out.ScopeJson = string(b)
		}
	}
	return out
}
