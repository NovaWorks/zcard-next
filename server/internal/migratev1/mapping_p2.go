package migratev1

// P2 目录域迁移：categories → products（+virtual_reviews 展开 / direct_content 重加密 /
// member_price key 重写）→ product_skus → reviews → supply_mappings 生成。
// 映射规格《数据迁移工具开发计划》§5.3；主站先行决策：merchant_id=1 → subsite_id=0，
// merchant_id>1 的分站数据跳过计数（P6 分批补迁）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/review"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
)

// MigrateCatalog P2 阶段。
func (m *Migrator) MigrateCatalog(ctx context.Context) error {
	if err := m.migrateCategories(ctx); err != nil {
		return err
	}
	if err := m.migrateProducts(ctx); err != nil {
		return err
	}
	if err := m.migrateProductSkus(ctx); err != nil {
		return err
	}
	if err := m.migrateReviews(ctx); err != nil {
		return err
	}
	return m.generateSupplyMappings(ctx)
}

// migrateCategories 1.x categories → categories。
// 1.x slug/description 无 2.0 列（丢弃入报告）；status=0 并入 hide。
func (m *Migrator) migrateCategories(ctx context.Context) error {
	var (
		id, sort, status int64
		merchant         int64
		parentID         sql.NullInt64
		name             string
		icon             sql.NullString
		hide             bool
	)
	return m.scanTable(ctx, "categories",
		[]string{"id", "merchant_id", "parent_id", "name", "icon", "sort", "status", "hide"},
		func() []any {
			return []any{&id, &merchant, &parentID, &name, &icon, &sort, &status, &hide}
		},
		func(int64) error {
			siteID, ok := m.subsiteFor(ctx, merchant)
			if !ok {
				m.st.Record("categories", "skip") // 分站分类：P6 补迁
				return nil
			}
			if _, ok := m.IDs.Get(ctx, "categories", uint64(id)); ok {
				m.st.Record("categories", "skip")
				return nil
			}
			b := m.Client.Category.Create().
				SetSubsiteID(siteID).
				SetName(truncateRunes(name, 60)).
				SetSort(int32(sort)).
				SetHide(hide || status == 0)
			if ic := strings.TrimSpace(nullStr(icon)); ic != "" {
				b.SetIcon(ic)
			}
			if pid := nullInt(parentID); pid > 0 {
				newPID, ok := m.IDs.Get(ctx, "categories", uint64(pid))
				if !ok {
					return fmt.Errorf("父分类 %d 未迁移（顺序异常或属分站）", pid)
				}
				b.SetParentID(newPID)
			}
			c, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "categories", uint64(id), c.ID); err != nil {
				return err
			}
			m.st.Record("categories", "migrated")
			return nil
		},
	)
}

// migrateProducts 1.x products → products。
// 关键变换：member_price JSON 的 key 由 1.x 用户组 ID 重写为 member_level 新 ID；
// fulfillment_type=fixed 的 delivery_message → direct_content（2.0 卡密钥匙 GCM，
// AAD=product/subsite，与 catalog 读取端口径一致）；软删 → 下架。
// 丢弃列（2.0 无 extra 列，统一报告）：seo_*/price_manual/upstream_price/stock_cache/
// upstream_product_url/min_order/max_order/contact_type/send_email/leave_message/
// only_user/purchase_limit/level_disable/pick_type/virtual_sales/virtual_reviews 聚合数。
func (m *Migrator) migrateProducts(ctx context.Context) error {
	var (
		id                      int64
		merchant, categoryID    sql.NullInt64
		name, slug              string
		description, cover      sql.NullString
		images                  sql.NullString
		price, factory, premium int64
		memberPrice             sql.NullString
		stockType               string
		fulfillmentType         sql.NullString
		deliveryMessage         sql.NullString
		stockVisible            bool
		controlConfig           sql.NullString
		deliveryMode            string
		dedup                   bool
		sort                    int64
		featured                bool
		status                  int64
		hide                    bool
		upstreamSource          sql.NullInt64
		upstreamCode            sql.NullString
		upstreamSynced          sql.NullString
		virtualReviews          sql.NullString
		deletedAt               sql.NullString
		createdAt, updatedAt    sql.NullString
	)
	return m.scanTable(ctx, "products",
		[]string{"id", "merchant_id", "category_id", "name", "slug", "description", "cover", "images",
			"price", "factory_price", "draft_premium", "member_price", "stock_type",
			"fulfillment_type", "delivery_message", "stock_visible", "control_config",
			"delivery_mode", "dedup", "sort", "is_featured", "status", "hide",
			"upstream_source_id", "upstream_product_code", "upstream_synced_at",
			"virtual_reviews", "deleted_at", "created_at", "updated_at"},
		func() []any {
			return []any{&id, &merchant, &categoryID, &name, &slug, &description, &cover, &images,
				&price, &factory, &premium, &memberPrice, &stockType,
				&fulfillmentType, &deliveryMessage, &stockVisible, &controlConfig,
				&deliveryMode, &dedup, &sort, &featured, &status, &hide,
				&upstreamSource, &upstreamCode, &upstreamSynced,
				&virtualReviews, &deletedAt, &createdAt, &updatedAt}
		},
		func(int64) error {
			siteID, siteOK := m.subsiteFor(ctx, nullInt(merchant))
			if !siteOK {
				m.st.Record("products", "skip") // 分站商品：P6 补迁
				return nil
			}
			if _, ok := m.IDs.Get(ctx, "products", uint64(id)); ok {
				m.st.Record("products", "skip")
				return nil
			}
			ca, _, err := mustTime(nullStr(createdAt), m.TZ)
			if err != nil {
				return err
			}
			ua, _, err := mustTime(nullStr(updatedAt), m.TZ)
			if err != nil {
				return err
			}
			st := int8(1)
			switch {
			case nullStr(deletedAt) != "":
				st = 0 // 软删 → 下架
			case hide:
				st = 2
			case status == 0:
				st = 0
			}
			b := m.Client.Product.Create().
				SetSubsiteID(siteID).
				SetName(truncateRunes(name, 1024)).
				SetSlug(safeSlug(slug, id)).
				SetPrice(price).
				SetFactoryPrice(factory).
				SetDraftPremium(premium).
				SetStockType(product.StockType(stockType)).
				SetStockVisible(stockVisible).
				SetDeliveryMode(product.DeliveryMode(deliveryMode)).
				SetDedup(dedup).
				SetSort(int32(sort)).
				SetIsRecommend(featured).
				SetStatus(st).
				SetCreatedAt(ca).
				SetUpdatedAt(ua)
			if cid := nullInt(categoryID); cid > 0 {
				newCID, ok := m.IDs.Get(ctx, "categories", uint64(cid))
				if !ok {
					return fmt.Errorf("分类 %d 未迁移（顺序异常或属分站）", cid)
				}
				b.SetCategoryID(newCID)
			}
			if d := nullStr(description); d != "" {
				b.SetDescription(d)
			}
			if c := nullStr(cover); c != "" {
				b.SetCover(c)
			}
			if img := nullStr(images); img != "" && json.Valid([]byte(img)) {
				var list []string
				if json.Unmarshal([]byte(img), &list) == nil {
					b.SetImages(list)
				}
			}
			if mp := nullStr(memberPrice); mp != "" && json.Valid([]byte(mp)) {
				rewritten, err := m.rewriteMemberPrice(mp)
				if err != nil {
					return err
				}
				if len(rewritten) > 0 {
					b.SetMemberPrice(rewritten)
				}
			}
			if cc := nullStr(controlConfig); cc != "" && json.Valid([]byte(cc)) {
				var cfg map[string]any
				if json.Unmarshal([]byte(cc), &cfg) == nil {
					b.SetControlConfig(cfg)
				}
			}
			if us := nullInt(upstreamSource); us > 0 {
				newUS, ok := m.IDs.Get(ctx, "supply_sources", uint64(us))
				if !ok {
					return fmt.Errorf("上游货源 %d 未迁移（顺序异常）", us)
				}
				b.SetUpstreamSourceID(newUS)
				if code := nullStr(upstreamCode); code != "" {
					if len(code) > 128 {
						return fmt.Errorf("upstream_product_code 超 128 字符：%s", truncate(code, 32))
					}
					b.SetUpstreamProductCode(code)
				}
				if t, ok, err := mustTime(nullStr(upstreamSynced), m.TZ); err != nil {
					return err
				} else if ok {
					b.SetUpstreamSyncedAt(t)
				}
			}
			p, err := b.Save(ctx)
			if err != nil {
				return err
			}
			// 直发内容（fulfillment_type=fixed）：明文 → 2.0 卡密钥匙 GCM（AAD=product/subsite）
			if nullStr(fulfillmentType) == "fixed" {
				msg := nullStr(deliveryMessage)
				if msg == "" {
					m.RW.AddError("products", uint64(id), "fixed 商品 delivery_message 为空，direct_content 未迁移")
				} else {
					cipher, err := inventory.NewCardCipher(m.NewCardKey)
					if err != nil {
						return fmt.Errorf("ZCARD_CARD_KEY 不可用（直发内容重加密必需）: %w", err)
					}
					sealed, err := cipher.Seal(msg, p.ID, p.SubsiteID)
					if err != nil {
						return err
					}
					if _, err := m.Client.Product.UpdateOne(p).SetDirectContent(sealed).Save(ctx); err != nil {
						return err
					}
				}
			}
			// 虚拟评论 list 展开（聚合数 rating/count 无 2.0 列，丢弃入报告）
			if vr := nullStr(virtualReviews); vr != "" && json.Valid([]byte(vr)) {
				if err := m.expandVirtualReviews(ctx, p.ID, vr); err != nil {
					return err
				}
			}
			if _, err := m.IDs.Put(ctx, m.Client, "products", uint64(id), p.ID); err != nil {
				return err
			}
			m.st.Record("products", "migrated")
			return nil
		},
	)
}

// rewriteMemberPrice {"<1.x组id>": 分} → {"<member_level新id>": 分}（映射缺失的 key 跳过并记录）。
func (m *Migrator) rewriteMemberPrice(raw string) (map[string]int64, error) {
	var src map[string]int64
	if err := json.Unmarshal([]byte(raw), &src); err != nil {
		return nil, fmt.Errorf("member_price 解析失败: %w", err)
	}
	out := make(map[string]int64, len(src))
	for k, cents := range src {
		var oldID int64
		if _, err := fmt.Sscanf(k, "%d", &oldID); err != nil {
			m.RW.AddError("products", 0, fmt.Sprintf("member_price key %q 非数字，已跳过", k))
			continue
		}
		newID, ok := m.IDs.Get(context.Background(), "user_groups", uint64(oldID))
		if !ok {
			m.RW.AddError("products", 0, fmt.Sprintf("member_price 引用未迁移用户组 %s，已跳过", k))
			continue
		}
		out[fmt.Sprintf("%d", newID)] = cents
	}
	return out, nil
}

// expandVirtualReviews 1.x virtual_reviews JSON（{"rating":4.8,"count":156,"list":[...]}）
// 的 list 数组展开为 virtual_reviews 行；无 list 仅聚合数则记报告。
func (m *Migrator) expandVirtualReviews(ctx context.Context, productID uint64, raw string) error {
	var doc struct {
		Rating float64          `json:"rating"`
		Count  int              `json:"count"`
		List   []map[string]any `json:"list"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil // 非预期结构：不阻断商品迁移，留报告
	}
	if len(doc.List) == 0 {
		m.RW.AddError("products", 0, fmt.Sprintf("商品 %d 虚拟评论仅聚合数(rating=%.1f,count=%d)，2.0 无对应列已丢弃", productID, doc.Rating, doc.Count))
		return nil
	}
	for i, item := range doc.List {
		nickname, _ := item["nickname"].(string)
		content, _ := item["content"].(string)
		rating := int8(5)
		if r, ok := item["rating"].(float64); ok && r >= 1 && r <= 5 {
			rating = int8(r)
		}
		if _, err := m.Client.VirtualReview.Create().
			SetProductID(productID).
			SetNickname(truncateRunes(nickname, 60)).
			SetContent(content).
			SetRating(rating).
			SetSort(int32(i)).
			Save(ctx); err != nil {
			return err
		}
	}
	return nil
}

// migrateProductSkus 1.x product_skus → product_skus。
// 1.x sort/status 无 2.0 列：status=0 跳过；spec_values 置空 map（1.x 无规格值概念）。
// upstream_sku_code 超 64 字符报错（截断会破坏上游同步键）。
func (m *Migrator) migrateProductSkus(ctx context.Context) error {
	var (
		id, productID, price, sort, status int64
		name                               string
		upstreamCode                       sql.NullString
	)
	_ = sort
	return m.scanTable(ctx, "product_skus",
		[]string{"id", "product_id", "name", "price", "upstream_sku_code", "sort", "status"},
		func() []any {
			return []any{&id, &productID, &name, &price, &upstreamCode, &sort, &status}
		},
		func(int64) error {
			if status == 0 {
				m.st.Record("product_skus", "skip") // 1.x 停用 SKU：不迁（2.0 无状态列）
				return nil
			}
			if _, ok := m.IDs.Get(ctx, "product_skus", uint64(id)); ok {
				m.st.Record("product_skus", "skip")
				return nil
			}
			newPID, ok := m.IDs.Get(ctx, "products", uint64(productID))
			if !ok {
				m.st.Record("product_skus", "skip") // 属分站商品（未迁）：跳过计数
				return nil
			}
			b := m.Client.ProductSku.Create().
				SetProductID(newPID).
				SetName(truncateRunes(name, 100)).
				SetSpecValues(map[string]string{}).
				SetPrice(price)
			if code := nullStr(upstreamCode); code != "" {
				if len(code) > 64 {
					return fmt.Errorf("upstream_sku_code 超 64 字符：%s", truncate(code, 32))
				}
				b.SetUpstreamSkuID(code)
			}
			s, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "product_skus", uint64(id), s.ID); err != nil {
				return err
			}
			m.st.Record("product_skus", "migrated")
			return nil
		},
	)
}

// migrateReviews 1.x reviews → reviews（状态枚举同名直传；user/order 缺失容忍为 0——
// 1.x 允许游客评价，2.0 user_id/order_id 为必填 uint64，0 值语义由查询侧兼容）。
func (m *Migrator) migrateReviews(ctx context.Context) error {
	var (
		id, productID, rating int64
		userID, orderID       sql.NullInt64
		content               sql.NullString
		status                string
		createdAt             sql.NullString
	)
	return m.scanTable(ctx, "reviews",
		[]string{"id", "product_id", "user_id", "order_id", "rating", "content", "status", "created_at"},
		func() []any {
			return []any{&id, &productID, &userID, &orderID, &rating, &content, &status, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "reviews", uint64(id)); ok {
				m.st.Record("reviews", "skip")
				return nil
			}
			newPID, ok := m.IDs.Get(ctx, "products", uint64(productID))
			if !ok {
				m.st.Record("reviews", "skip") // 分站商品评论：P6 补
				return nil
			}
			ca, caOK, err := mustTime(nullStr(createdAt), m.TZ)
			if err != nil {
				return err
			}
			b := m.Client.Review.Create().
				SetProductID(newPID).
				SetUserID(uint64(nullInt(userID))).
				SetOrderID(uint64(nullInt(orderID))).
				SetRating(int8(rating)).
				SetContent(nullStr(content)).
				SetStatus(review.Status(status))
			if caOK { // 直插行可能缺时间（零日期）——不 Set 走 ent 默认
				b = b.SetCreatedAt(ca)
			}
			r, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "reviews", uint64(id), r.ID); err != nil {
				return err
			}
			m.st.Record("reviews", "migrated")
			return nil
		},
	)
}

// generateSupplyMappings 为已迁移的上游商品生成 supply_mappings（幂等：存在即跳过）。
// up_stock 不回填（2.0 同步任务接管）。
func (m *Migrator) generateSupplyMappings(ctx context.Context) error {
	rows, err := m.Client.Product.Query().
		Where(product.UpstreamSourceIDNotNil()).
		All(ctx)
	if err != nil {
		return err
	}
	t := m.st.table("supply_mappings")
	for _, p := range rows {
		if p.UpstreamProductCode == "" {
			continue
		}
		exists, err := m.Client.SupplyMapping.Query().
			Where(
				supplymapping.ConnectionID(p.UpstreamSourceID),
				supplymapping.UpstreamProduct(p.UpstreamProductCode),
				supplymapping.UpstreamSku(""),
			).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			t.SkippedExists++
			continue
		}
		if _, err := m.Client.SupplyMapping.Create().
			SetConnectionID(p.UpstreamSourceID).
			SetUpstreamProduct(p.UpstreamProductCode).
			SetUpstreamSku("").
			SetLocalProductID(p.ID).
			Save(ctx); err != nil {
			return err
		}
		t.Migrated++
	}
	return nil
}

// safeSlug 1.x slug 最长 250，2.0 上限 150：超长按 rune 截 140 后追加 -v1-<旧ID>
// （保唯一与可追溯），正常长度原样。
func safeSlug(slug string, oldID int64) string {
	if utf8.RuneCountInString(slug) <= 150 {
		return slug
	}
	runes := []rune(slug)
	return string(runes[:140]) + fmt.Sprintf("-v1-%d", oldID)
}

// truncateRunes 按 rune 截断（多字节安全）。
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
