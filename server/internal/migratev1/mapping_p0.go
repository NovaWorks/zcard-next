package migratev1

// P0 系统域迁移：currencies / user_groups→member_levels(+UserGroup 兼容表) /
// supply_sources→supply_connections / settings 首批映射。
// 映射规格《数据迁移工具开发计划》§5.1；driver 枚举两端同名直传（dujiao_next/acg_faka/zcard）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/currency"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// MigrateSystem P0 阶段。
func (m *Migrator) MigrateSystem(ctx context.Context) error {
	if err := m.migrateCurrencies(ctx); err != nil {
		return err
	}
	if err := m.migrateUserGroups(ctx); err != nil {
		return err
	}
	if err := m.migrateSupplySources(ctx); err != nil {
		return err
	}
	return m.migrateSettings(ctx)
}

// migrateCurrencies code 为唯一键（v1id_maps 不适用）：存在则更新汇率/展示参数，否则插入。
// 1.x is_base 无 2.0 列（基础货币在 2.0 侧另行约定），非 CNY 基础货币仅进报告提示。
func (m *Migrator) migrateCurrencies(ctx context.Context) error {
	var (
		id                        int64
		code, symbol              string
		name, pos, rate           string
		decimalPlaces, sort, enab int64
		isBase                    bool
	)
	_ = name // 1.x 币种名无 2.0 列，显式丢弃
	return m.scanTable(ctx, "currencies",
		[]string{"id", "code", "name", "symbol", "symbol_position", "decimal_places", "exchange_rate", "is_base", "is_enabled", "sort"},
		func() []any {
			return []any{&id, &code, &name, &symbol, &pos, &decimalPlaces, &rate, &isBase, &enab, &sort}
		},
		func(int64) error {
			position := "prefix"
			if strings.TrimSpace(pos) == "after" {
				position = "suffix"
			}
			rateF, _ := strconv.ParseFloat(strings.TrimSpace(rate), 64)
			row, err := m.Client.Currency.Query().Where(currency.Code(code)).Only(ctx)
			if err == nil {
				if _, err := m.Client.Currency.UpdateOne(row).
					SetSymbol(symbol).
					SetPosition(currency.Position(position)).
					SetPrecision(int32(decimalPlaces)).
					SetRate(rateF).
					SetEnabled(enab != 0).
					SetSort(int32(sort)).
					Save(ctx); err != nil {
					return err
				}
				m.st.Record("currencies", "skip")
				return nil
			}
			if !ent.IsNotFound(err) {
				return err
			}
			if _, err := m.Client.Currency.Create().
				SetCode(code).
				SetSymbol(symbol).
				SetPosition(currency.Position(position)).
				SetPrecision(int32(decimalPlaces)).
				SetRate(rateF).
				SetEnabled(enab != 0).
				SetSort(int32(sort)).
				Save(ctx); err != nil {
				return err
			}
			if isBase {
				m.RW.AddError("currencies", uint64(id), fmt.Sprintf("基础货币 %s 无 2.0 直迁列（请在 2.0 核对基础货币约定）", code))
			}
			m.st.Record("currencies", "migrated")
			return nil
		},
	)
}

// migrateUserGroups 1.x user_groups → member_levels + UserGroup 兼容表（双写）。
// discount：1.x decimal 百分数（100.00=原价）→ 2.0 万分比（10000）。
// 用户显式组不落列（2.0 等级由累计充值/消费阈值实时推导），交易域迁完后自动恢复。
func (m *Migrator) migrateUserGroups(ctx context.Context) error {
	var (
		id, sort, status            int64
		name, discountStr           string
		minRecharge, minConsumption int64
	)
	return m.scanTable(ctx, "user_groups",
		[]string{"id", "name", "discount", "min_recharge", "min_consumption", "sort", "status"},
		func() []any {
			return []any{&id, &name, &discountStr, &minRecharge, &minConsumption, &sort, &status}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "user_groups", uint64(id)); ok {
				m.st.Record("user_groups", "skip")
				return nil
			}
			discount, err := strconv.ParseFloat(strings.TrimSpace(discountStr), 64)
			if err != nil {
				return fmt.Errorf("discount %q 解析失败: %w", discountStr, err)
			}
			thresholdType := memberlevel.ThresholdTypeRecharge
			switch {
			case minRecharge > 0 && minConsumption > 0:
				thresholdType = memberlevel.ThresholdTypeBothOr
			case minConsumption > 0:
				thresholdType = memberlevel.ThresholdTypeConsume
			}
			lv, err := m.Client.MemberLevel.Create().
				SetName(name).
				SetThresholdType(thresholdType).
				SetThresholdRecharge(minRecharge).
				SetThresholdConsume(minConsumption).
				SetDiscount(int32(discount * 100)).
				SetSort(int32(sort)).
				SetEnabled(status != 0).
				Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.Client.UserGroup.Create().
				SetCode(fmt.Sprintf("v1-%d", id)).
				SetName(name).
				SetLevelID(lv.ID).
				Save(ctx); err != nil {
				return fmt.Errorf("UserGroup 兼容表写入失败: %w", err)
			}
			if _, err := m.IDs.Put(ctx, m.Client, "user_groups", uint64(id), lv.ID); err != nil {
				return err
			}
			m.st.Record("user_groups", "migrated")
			return nil
		},
	)
}

// migrateSupplySources 1.x supply_sources → supply_connections。
// credentials：APP_KEY 解密（容错历史明文）→ DataBox AES-256-GCM 重加密。
func (m *Migrator) migrateSupplySources(ctx context.Context) error {
	var (
		id                          int64
		name, driver, baseURL, cred string
		status                      string
		settingsJSON, lastSynced    sql.NullString
		lastErr, deletedAt          sql.NullString
		balance                     sql.NullInt64
	)
	return m.scanTable(ctx, "supply_sources",
		[]string{"id", "name", "driver", "base_url", "credentials", "status", "settings",
			"balance_cache", "last_synced_at", "last_error", "deleted_at"},
		func() []any {
			return []any{&id, &name, &driver, &baseURL, &cred, &status, &settingsJSON,
				&balance, &lastSynced, &lastErr, &deletedAt} // 可空列一律 Null 扫描
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "supply_sources", uint64(id)); ok {
				m.st.Record("supply_sources", "skip")
				return nil
			}
			plain := cred
			if laracrypt.LooksEncrypted(cred) {
				if m.AppKey == nil {
					return fmt.Errorf("credentials 为密文但 APP_KEY 不可用")
				}
				c, err := laracrypt.New(m.AppKey)
				if err != nil {
					return err
				}
				if plain, err = c.OpenString(cred); err != nil {
					return fmt.Errorf("credentials 解密失败: %w", err)
				}
			}
			if plain != "" && !json.Valid([]byte(plain)) {
				return fmt.Errorf("credentials 明文非 JSON：%q", truncate(plain, 32))
			}
			box, err := crypto.NewBox(m.DataKey)
			if err != nil {
				return fmt.Errorf("ZCARD_DATA_KEY 不可用（凭据重加密必需）: %w", err)
			}
			sealed, err := box.Seal([]byte(plain), nil)
			if err != nil {
				return err
			}
			var settings map[string]any
			if sj := nullStr(settingsJSON); sj != "" && json.Valid([]byte(sj)) {
				_ = json.Unmarshal([]byte(sj), &settings)
			}
			st := supplyconnection.StatusActive
			if status == "disabled" || nullStr(deletedAt) != "" {
				st = supplyconnection.StatusDisabled
			}
			b := m.Client.SupplyConnection.Create().
				SetName(name).
				SetDriver(driver).
				SetBaseURL(baseURL).
				SetCredentials(sealed).
				SetStatus(st).
				SetBalanceCache(nullInt(balance)).
				SetSettings(settings)
			if t, ok, err := mustTime(nullStr(lastSynced), m.TZ); err != nil {
				return err
			} else if ok {
				b.SetLastSyncedAt(t)
			}
			if le := strings.TrimSpace(nullStr(lastErr)); le != "" {
				b.SetLastError(le)
			}
			conn, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "supply_sources", uint64(id), conn.ID); err != nil {
				return err
			}
			m.st.Record("supply_sources", "migrated")
			return nil
		},
	)
}

// migrateSettings 1.x settings（key-value json）→ 2.0 settings(group,key)。
// 命中映射表 → 目标组；SECRET/弃用 → 跳过（报告提示重配）；其余 → group=v1_legacy 保真。
func (m *Migrator) migrateSettings(ctx context.Context) error {
	var (
		id  int64
		k   string
		v   sql.NullString
		grp sql.NullString
	)
	return m.scanTable(ctx, "settings",
		[]string{"id", "key", "value", "group"},
		func() []any { return []any{&id, &k, &v, &grp} },
		func(int64) error {
			if reason, skip := settingsSkipped[k]; skip {
				m.RW.AddError("settings", uint64(id), fmt.Sprintf("跳过 %s：%s", k, reason))
				m.st.Record("settings", "skip")
				return nil
			}
			rule, hit := settingsMap[k]
			g, key := "v1_legacy", k
			var value json.RawMessage
			if hit {
				g, key = rule.Group, rule.Key
				nv, err := normalizeSettingValue(rule, nullStr(v))
				if err != nil {
					return fmt.Errorf("settings %s 值转换失败: %w", k, err)
				}
				value = nv
			} else {
				value = json.RawMessage(nullStr(v)) // 原样保真（1.x value 本就是 JSON 文档；NULL → 空 JSON）
			}
			exists, err := m.Client.Setting.Query().
				Where(setting.Group(g), setting.Key(key)).Exist(ctx)
			if err != nil {
				return err
			}
			if exists {
				m.st.Record("settings", "skip")
				return nil
			}
			if _, err := m.Client.Setting.Create().
				SetGroup(g).
				SetKey(key).
				SetValue(value).
				Save(ctx); err != nil {
				return err
			}
			m.st.Record("settings", "migrated")
			return nil
		},
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
