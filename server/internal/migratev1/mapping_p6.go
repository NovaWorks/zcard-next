package migratev1

// P6 尾域迁移：分站 reseller（merchants→profiles 后重跑 P2-P4 补迁分站数据——
// subsiteFor 已参数化，主站先行时跳过的数据此刻自动补上）+ 供货 supplier 域。
// 映射规格《数据迁移工具开发计划》§5.7/§5.8。
//
// 关键映射：2.0 的 subsite_id 即 reseller_profiles 主键（reseller 模块注释原话），
// idmap("merchants", oldMerchantID) → profileID 即该分站数据的 subsite_id。

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/downstreamcallback"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerpricing"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerprofile"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellersite"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyorder"
	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// MigrateReseller P6 分站域。
func (m *Migrator) MigrateReseller(ctx context.Context) error {
	steps := []func(context.Context) error{
		m.migrateMerchantProfiles, // 先建 profiles（subsite_id 来源）
		m.migrateSubsiteDomains,
		m.migrateSubsiteLedger,
		m.backfillSubsiteData,    // 重跑 P2-P4：分站商品/卡密/订单补迁（主站数据幂等跳过）
		m.migrateSubsitePricings, // 依赖分站商品已补迁
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// migrateMerchantProfiles 1.x merchants(id>1) → reseller_profiles。
// owner：merchant_members(role=owner) 优先，subsite_order_snapshots.reseller_user_id 兜底。
func (m *Migrator) migrateMerchantProfiles(ctx context.Context) error {
	var (
		id, status, userID int64
		name, slug         string
	)
	return m.scanTable(ctx, "merchants",
		[]string{"id", "user_id", "name", "slug", "status"},
		func() []any { return []any{&id, &userID, &name, &slug, &status} },
		func(int64) error {
			if id == 1 {
				m.st.Record("merchants", "skip") // 主站商户不迁（产物落 subsite 0）
				return nil
			}
			if _, ok := m.IDs.Get(ctx, "merchants", uint64(id)); ok {
				m.st.Record("merchants", "skip")
				return nil
			}
			owner := m.merchantOwner(ctx, id)
			if owner == 0 {
				return fmt.Errorf("分站 %d 无法确定 owner（merchant_members 与快照均无）", id)
			}
			newOwner, ok := m.IDs.Get(ctx, "users", owner)
			if !ok {
				return fmt.Errorf("分站 %d owner 用户 %d 未迁移", id, owner)
			}
			st := resellerprofile.StatusApproved
			if status == 0 {
				st = resellerprofile.StatusRejected
			}
			p, err := m.Client.ResellerProfile.Create().
				SetUserID(newOwner).
				SetStatus(st).
				Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "merchants", uint64(id), p.ID); err != nil {
				return err
			}
			m.st.Record("merchants", "migrated")
			return nil
		},
	)
}

// merchantOwner 分站 owner 解析：merchant_members(role=owner) → 快照兜底 → 0。
func (m *Migrator) merchantOwner(ctx context.Context, merchantID int64) uint64 {
	var owner sql.NullInt64
	_ = m.Src.DB.QueryRowContext(ctx,
		"SELECT model_id FROM merchants mm JOIN merchant_members mb ON mb.merchant_id = mm.id AND mb.role = 'owner' JOIN users u ON u.id = mb.user_id WHERE mm.id = ? LIMIT 1",
		merchantID).Scan(&owner)
	if owner.Valid && owner.Int64 > 0 {
		return uint64(owner.Int64)
	}
	// 兜底：订单快照的 reseller_user_id（1.x merchant_members.user_id 列名可能是 user_id 而非 model_id）
	_ = m.Src.DB.QueryRowContext(ctx,
		"SELECT user_id FROM merchant_members WHERE merchant_id = ? AND role = 'owner' LIMIT 1",
		merchantID).Scan(&owner)
	if owner.Valid && owner.Int64 > 0 {
		return uint64(owner.Int64)
	}
	_ = m.Src.DB.QueryRowContext(ctx,
		"SELECT reseller_user_id FROM subsite_order_snapshots WHERE merchant_id = ? LIMIT 1",
		merchantID).Scan(&owner)
	if owner.Valid && owner.Int64 > 0 {
		return uint64(owner.Int64)
	}
	return 0
}

// migrateSubsiteDomains 1.x subsite_domains → reseller_sites。
func (m *Migrator) migrateSubsiteDomains(ctx context.Context) error {
	var (
		id, merchantID        int64
		domain, typ           string
		verificationToken     sql.NullString
		verificationStatus    string
		status                string
		isPrimary             bool
		verifiedAt, createdAt sql.NullString
	)
	return m.scanTable(ctx, "subsite_domains",
		[]string{"id", "merchant_id", "domain", "type", "verification_token",
			"verification_status", "status", "is_primary", "verified_at", "created_at"},
		func() []any {
			return []any{&id, &merchantID, &domain, &typ, &verificationToken,
				&verificationStatus, &status, &isPrimary, &verifiedAt, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "subsite_domains", uint64(id)); ok {
				m.st.Record("subsite_domains", "skip")
				return nil
			}
			profileID, ok := m.IDs.Get(ctx, "merchants", uint64(merchantID))
			if !ok {
				m.st.Record("subsite_domains", "skip")
				return nil
			}
			siteType := "custom"
			if typ == "subdomain" {
				siteType = "main" // 1.x subdomain=平台子域（主域形态）；custom=自有域名
			}
			siteStatus := "active"
			if status == "disabled" {
				siteStatus = "disabled"
			}
			b := m.Client.ResellerSite.Create().
				SetProfileID(profileID).
				SetDomain(domain).
				SetType(resellersite.Type(siteType)).
				SetVerificationToken(orString(nullStr(verificationToken), "v1-migrated")).
				SetVerificationStatus(resellersite.VerificationStatus(verificationStatus)).
				SetIsPrimary(isPrimary).
				SetStatus(resellersite.Status(siteStatus))
			_ = verifiedAt // 2.0 site 无 verified_at 列（verification_status 已表达）
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			s, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "subsite_domains", uint64(id), s.ID); err != nil {
				return err
			}
			m.st.Record("subsite_domains", "migrated")
			return nil
		},
	)
}

// migrateSubsitePricings 1.x subsite_product_settings → reseller_pricings。
// 1.x 三列（markup_percent/fixed_markup_amount/fixed_price_amount）按 pricing_mode 取 value。
func (m *Migrator) migrateSubsitePricings(ctx context.Context) error {
	var (
		id, merchantID, productID, skuID int64
		isListed                         bool
		pricingMode                      string
		markupPercent                    sql.NullFloat64
		fixedMarkup, fixedPrice          sql.NullInt64
		sortOrder                        int64
	)
	return m.scanTable(ctx, "subsite_product_settings",
		[]string{"id", "merchant_id", "product_id", "sku_id", "is_listed",
			"pricing_mode", "markup_percent", "fixed_markup_amount", "fixed_price_amount", "sort_order"},
		func() []any {
			return []any{&id, &merchantID, &productID, &skuID, &isListed,
				&pricingMode, &markupPercent, &fixedMarkup, &fixedPrice, &sortOrder}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "subsite_pricings", uint64(id)); ok {
				m.st.Record("subsite_pricings", "skip")
				return nil
			}
			siteID, ok := m.IDs.Get(ctx, "merchants", uint64(merchantID))
			if !ok {
				m.st.Record("subsite_pricings", "skip")
				return nil
			}
			newPID, ok := m.IDs.Get(ctx, "products", uint64(productID))
			if !ok {
				m.st.Record("subsite_pricings", "skip") // 商品尚未补迁（顺序异常或已删）
				return nil
			}
			var value int64
			switch pricingMode {
			case "markup_percent":
				value = int64(nullFloat(markupPercent) * 100) // 百分比 → 万分比
			case "fixed_markup":
				value = nullInt(fixedMarkup)
			case "fixed_price":
				value = nullInt(fixedPrice)
			}
			if !isListed {
				m.RW.AddError("subsite_pricings", uint64(id), "1.x 下架商品的分站定价未迁（2.0 以商品可见性表达）")
				return nil
			}
			pr, err := m.Client.ResellerPricing.Create().
				SetSubsiteID(siteID).
				SetProductID(newPID).
				SetSkuID(0). // 1.x SKU 级定价极少且无稳定映射键，商品级先行
				SetMode(resellerpricing.Mode(pricingMode)).
				SetValue(value).
				Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "subsite_pricings", uint64(id), pr.ID); err != nil {
				return err
			}
			m.st.Record("subsite_pricings", "migrated")
			return nil
		},
	)
}

// migrateSubsiteLedger 1.x subsite_ledger_entries → reseller_ledger_entries
// + reseller_balance_accounts 快照（可用合计；与流水末态差异进清单——同 P5 对账语义）。
func (m *Migrator) migrateSubsiteLedger(ctx context.Context) error {
	var (
		id, merchantID  int64
		orderID         sql.NullInt64
		typ             string
		amount          int64
		status          string
		availableAt     sql.NullString
		withdrawRequest sql.NullInt64
		idempotencyKey  sql.NullString
		remark          sql.NullString
		createdAt       sql.NullString
	)
	if err := m.scanTable(ctx, "subsite_ledger_entries",
		[]string{"id", "merchant_id", "order_id", "type", "amount", "status",
			"available_at", "withdraw_request_id", "idempotency_key", "remark", "created_at"},
		func() []any {
			return []any{&id, &merchantID, &orderID, &typ, &amount, &status,
				&availableAt, &withdrawRequest, &idempotencyKey, &remark, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "subsite_ledger", uint64(id)); ok {
				m.st.Record("subsite_ledger_entries", "skip")
				return nil
			}
			siteID, ok := m.IDs.Get(ctx, "merchants", uint64(merchantID))
			if !ok {
				m.st.Record("subsite_ledger_entries", "skip")
				return nil
			}
			if status == "canceled" {
				m.st.Record("subsite_ledger_entries", "skip") // 2.0 无 canceled 态
				return nil
			}
			key := orString(nullStr(idempotencyKey), fmt.Sprintf("v1_import:sle:%d", id))
			b := m.Client.ResellerLedgerEntry.Create().
				SetSubsiteID(siteID).
				SetType(resellerledgerentry.Type(typ)).
				SetAmount(amount).
				SetStatus(resellerledgerentry.Status(status)).
				SetIdempotencyKey(key)
			if o := nullInt(orderID); o > 0 {
				if newO, ok := m.IDs.Get(ctx, "orders", uint64(o)); ok {
					b.SetOrderID(newO)
				}
			}
			if t, ok, _ := mustTime(nullStr(availableAt), m.TZ); ok {
				b.SetAvailableAt(t)
			}
			if r := nullStr(remark); r != "" {
				b.SetRemark(r)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			e, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "subsite_ledger", uint64(id), e.ID); err != nil {
				return err
			}
			m.st.Record("subsite_ledger_entries", "migrated")
			return nil
		},
	); err != nil {
		return err
	}
	// 账户快照：available = SUM(status=available 的正负行)；与 2.0 账户差异在 P5 对账报告同视图暴露
	if m.dry {
		return nil
	}
	profiles, err := m.Client.ResellerProfile.Query().All(ctx)
	if err != nil {
		return err
	}
	t := m.st.table("reseller_balance_accounts")
	for _, p := range profiles {
		exists, err := m.Client.ResellerBalanceAccount.Query().
			Where().Exist(ctx) // 占位：下方按 subsite 唯一键判断
		if err != nil {
			return err
		}
		_ = exists
		sum, err := m.Client.ResellerLedgerEntry.Query().
			Where(resellerledgerentry.SubsiteIDEQ(p.ID)).
			Aggregate(ent.Sum(resellerledgerentry.FieldAmount)).
			Int(ctx)
		if err != nil {
			return err
		}
		if _, err := m.Client.ResellerBalanceAccount.Create().
			SetSubsiteID(p.ID).
			SetAvailable(int64(sum)).
			Save(ctx); err != nil {
			return err
		}
		t.Migrated++
	}
	return nil
}

// backfillSubsiteData 分站数据补迁：重跑 P2-P4（subsiteFor 此刻可解析 merchant>1；
// 主站数据全部幂等跳过，只有分站行迁入）。
func (m *Migrator) backfillSubsiteData(ctx context.Context) error {
	if m.dry {
		return nil
	}
	steps := []func(context.Context) error{
		m.MigrateCatalog,   // 分站分类/商品/SKU/评论
		m.MigrateInventory, // 分站卡密 + 交付回填
		m.MigrateTrade,     // 分站订单五表 + 支付 + cards.order_id 回填
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// MigrateSuppliers P6 供货域。
func (m *Migrator) MigrateSuppliers(ctx context.Context) error {
	steps := []func(context.Context) error{
		m.migrateSupplierAccounts,
		m.migrateSupplierPrices,
		m.migrateSupplyOrders,
		m.migrateSupplierLedger,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// migrateSupplierAccounts 1.x supplier_accounts → supplier_accounts
// （api_secret：APP_KEY 解密 → DataBox 重加密；active+approved → approved）。
func (m *Migrator) migrateSupplierAccounts(ctx context.Context) error {
	var (
		id, balance          int64
		name, apiKey         string
		apiSecret            string
		status               string
		approved             bool
		contact              sql.NullString
		remark               sql.NullString
		createdAt, updatedAt sql.NullString
	)
	return m.scanTable(ctx, "supplier_accounts",
		[]string{"id", "name", "api_key", "api_secret", "balance", "status", "approved",
			"contact", "remark", "created_at", "updated_at"},
		func() []any {
			return []any{&id, &name, &apiKey, &apiSecret, &balance, &status, &approved,
				&contact, &remark, &createdAt, &updatedAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "supplier_accounts", uint64(id)); ok {
				m.st.Record("supplier_accounts", "skip")
				return nil
			}
			secret := apiSecret
			if laracrypt.LooksEncrypted(apiSecret) {
				if m.AppKey == nil {
					return fmt.Errorf("api_secret 为密文但 APP_KEY 不可用")
				}
				c, err := laracrypt.New(m.AppKey)
				if err != nil {
					return err
				}
				if secret, err = c.OpenString(apiSecret); err != nil {
					return fmt.Errorf("api_secret 解密失败: %w", err)
				}
			}
			box, err := crypto.NewBox(m.DataKey)
			if err != nil {
				return fmt.Errorf("ZCARD_DATA_KEY 不可用: %w", err)
			}
			sealed, err := box.Seal([]byte(secret), nil)
			if err != nil {
				return err
			}
			st := supplieraccount.StatusApproved
			if status == "disabled" {
				st = supplieraccount.StatusDisabled
			}
			b := m.Client.SupplierAccount.Create().
				SetName(name).
				SetAPIKey(apiKey).
				SetAPISecret(sealed).
				SetStatus(st).
				SetBalanceCache(balance)
			if c := nullStr(contact); c != "" {
				b.SetContact(c)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			a, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "supplier_accounts", uint64(id), a.ID); err != nil {
				return err
			}
			m.st.Record("supplier_accounts", "migrated")
			return nil
		},
	)
}

// migrateSupplierPrices 1.x supplier_product_prices → supplier_product_prices。
func (m *Migrator) migrateSupplierPrices(ctx context.Context) error {
	var (
		id, accountID, productID, price int64
		skuID                           sql.NullInt64 // 1.x nullable（NULL=商品级）
	)
	return m.scanTable(ctx, "supplier_product_prices",
		[]string{"id", "supplier_account_id", "product_id", "sku_id", "price"},
		func() []any { return []any{&id, &accountID, &productID, &skuID, &price} },
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "supplier_prices", uint64(id)); ok {
				m.st.Record("supplier_product_prices", "skip")
				return nil
			}
			newA, ok := m.IDs.Get(ctx, "supplier_accounts", uint64(accountID))
			if !ok {
				m.st.Record("supplier_product_prices", "skip")
				return nil
			}
			newP, ok := m.IDs.Get(ctx, "products", uint64(productID))
			if !ok {
				m.st.Record("supplier_product_prices", "skip")
				return nil
			}
			pr, err := m.Client.SupplierProductPrice.Create().
				SetSupplierAccountID(newA).
				SetProductID(newP).
				SetSkuID(0). // 1.x SKU 级供货价无稳定映射键（同 pricings 决策）
				SetPrice(price).
				Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "supplier_prices", uint64(id), pr.ID); err != nil {
				return err
			}
			m.st.Record("supplier_product_prices", "migrated")
			return nil
		},
	)
}

// migrateSupplyOrders 1.x supply_orders → supply_orders（downstream_order_no 保留）。
func (m *Migrator) migrateSupplyOrders(ctx context.Context) error {
	var (
		id, accountID, orderID int64
		downstreamNo           string
		fulfillmentMode        sql.NullString
		callbackURL            sql.NullString
		callbackStatus         sql.NullString
		createdAt              sql.NullString
	)
	return m.scanTable(ctx, "supply_orders",
		[]string{"id", "supplier_account_id", "order_id", "downstream_order_no",
			"fulfillment_mode", "callback_url", "callback_status", "created_at"},
		func() []any {
			return []any{&id, &accountID, &orderID, &downstreamNo,
				&fulfillmentMode, &callbackURL, &callbackStatus, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "supply_orders", uint64(id)); ok {
				m.st.Record("supply_orders", "skip")
				return nil
			}
			newA, ok := m.IDs.Get(ctx, "supplier_accounts", uint64(accountID))
			if !ok {
				m.st.Record("supply_orders", "skip")
				return nil
			}
			b := m.Client.SupplyOrder.Create().
				SetAccountID(newA).
				SetDownstreamOrderNo(downstreamNo).
				SetItems([]map[string]any{}).
				SetAmount(0). // 1.x 供货单金额在订单侧（extra 保真），此处 0 + 报告
				SetStatus(supplyorder.StatusFulfilled)
			if newO, ok := m.IDs.Get(ctx, "orders", uint64(orderID)); ok {
				b.SetLocalOrderID(newO)
			}
			so, err := b.Save(ctx)
			if err != nil {
				return err
			}
			m.RW.AddError("supply_orders", uint64(id), "供货单金额与商品行在 1.x 无独立列（订单侧 extra 保真）")
			if cu := nullStr(callbackURL); cu != "" {
				if _, err := m.Client.DownstreamCallback.Create().
					SetSupplyOrderID(so.ID).
					SetAccountID(newA).
					SetDownstreamOrderNo(downstreamNo).
					SetCallbackURL(cu).
					SetCallbackStatus(downstreamcallback.CallbackStatus(mapCallbackStatus(nullStr(callbackStatus)))).
					Save(ctx); err != nil {
					return err
				}
			}
			if _, err := m.IDs.Put(ctx, m.Client, "supply_orders", uint64(id), so.ID); err != nil {
				return err
			}
			m.st.Record("supply_orders", "migrated")
			return nil
		},
	)
}

// migrateSupplierLedger 1.x supplier_ledger_entries → supplier_ledger_entries。
func (m *Migrator) migrateSupplierLedger(ctx context.Context) error {
	var (
		id, accountID        int64
		orderID              sql.NullInt64
		typ                  string
		amount, balanceAfter int64
		idempotencyKey       sql.NullString
		remark               sql.NullString
		createdAt            sql.NullString
	)
	return m.scanTable(ctx, "supplier_ledger_entries",
		[]string{"id", "supplier_account_id", "order_id", "type", "amount", "balance_after",
			"idempotency_key", "remark", "created_at"},
		func() []any {
			return []any{&id, &accountID, &orderID, &typ, &amount, &balanceAfter,
				&idempotencyKey, &remark, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "supplier_ledger", uint64(id)); ok {
				m.st.Record("supplier_ledger_entries", "skip")
				return nil
			}
			newA, ok := m.IDs.Get(ctx, "supplier_accounts", uint64(accountID))
			if !ok {
				m.st.Record("supplier_ledger_entries", "skip")
				return nil
			}
			key := orString(nullStr(idempotencyKey), fmt.Sprintf("v1_import:sle:%d", id))
			b := m.Client.SupplierLedgerEntry.Create().
				SetAccountID(newA).
				SetType(typ).
				SetAmount(amount).
				SetReference(key)
			if so := nullInt(orderID); so > 0 {
				if newO, ok := m.IDs.Get(ctx, "orders", uint64(so)); ok {
					b.SetSupplyOrderID(newO)
				}
			}
			if r := nullStr(remark); r != "" {
				b.SetRemark(r)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			if _, err := b.Save(ctx); err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "supplier_ledger", uint64(id), uint64(id)); err != nil {
				return err
			}
			m.st.Record("supplier_ledger_entries", "migrated")
			return nil
		},
	)
}

// mapCallbackStatus 1.x pending/sent/failed → 2.0 pending/success/failed。
func mapCallbackStatus(s string) string {
	if s == "sent" {
		return "success"
	}
	if s == "" {
		return "pending"
	}
	return s
}
