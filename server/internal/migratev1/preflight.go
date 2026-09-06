package migratev1

// preflight 巡检：源库连通性 / 表完整性 / schema 版本探测 / 密钥解析与抽样自检。
// 任何 FAIL 即拒绝迁移（不带病迁移，《数据迁移工具开发计划》§2.2）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"
)

// CheckStatus 巡检项状态。
type CheckStatus string

const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
	StatusInfo CheckStatus = "info"
)

// Check 单项巡检结果。
type Check struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

// PreflightOptions 巡检输入。
type PreflightOptions struct {
	// Env 1.x .env 键集（--old-env 解析产物；可为 nil）。
	Env map[string]string
	// AppKeyRaw / CardKeyRaw 显式密钥串（--old-app-key / --old-card-key）。
	AppKeyRaw, CardKeyRaw string
	// SampleLimit 卡密抽样行数（默认 10）。
	SampleLimit int
	// SkipMAC 抢救场景跳过 MAC 校验（正常路径必须校验）。
	SkipMAC bool
}

// PreflightReport 巡检报告。
type PreflightReport struct {
	GeneratedAt   time.Time
	ServerVersion string
	Checks        []Check

	// 密钥解析结果（后续阶段直接复用，避免重复解析）。
	AppKey     []byte `json:"-"`
	AppKeyRaw  string // 脱敏展示用（来源说明）
	CardKey    []byte `json:"-"`
	CardKeyRaw string

	// 卡密抽样统计。
	CardSampled       int
	CardEncrypted     int
	CardPlaintext     int
	CardDecryptFailed int

	// 规模概览（信息级）。
	TableCounts map[string]int64

	// Timezone 1.x 时区口径（APP_TIMEZONE；空 = 未提供，按 UTC 处理）。
	Timezone string
}

// HasFail 是否存在致命项。
func (r *PreflightReport) HasFail() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

func (r *PreflightReport) add(status CheckStatus, name, msg string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Message: msg})
}

// requiredTables 缺失即 FAIL 的核心表；optionalTables 缺失仅 WARN。
var (
	requiredTables = []string{
		"users", "user_groups", "categories", "products", "product_skus",
		"card_imports", "cards", "orders", "order_items", "order_deliveries",
		"payments", "payment_channels", "recharges", "coupons", "reviews",
		"bills", "withdrawals", "commissions", "merchants", "merchant_members",
		"currencies", "settings", "media", "media_categories",
		"supply_sources", "supplier_accounts", "supplier_product_prices",
		"supplier_ledger_entries", "supply_orders",
		"subsite_domains", "subsite_product_settings", "subsite_ledger_entries", "subsite_order_snapshots",
	}
	optionalTables = []string{"supply_sync_tasks", "security_audit_logs", "visit_logs"}
)

// integerTypes 「元→分」改造后应为整型族的列（探测旧 schema 用）。
var integerTypes = map[string]bool{
	"tinyint": true, "smallint": true, "mediumint": true, "int": true, "bigint": true,
}

// RunPreflight 执行巡检。返回报告；报告内含 FAIL 项时由调用方决定退出。
func RunPreflight(ctx context.Context, src *Source, opt PreflightOptions) (*PreflightReport, error) {
	if opt.SampleLimit <= 0 {
		opt.SampleLimit = 10
	}
	r := &PreflightReport{
		GeneratedAt: time.Now(),
		TableCounts: map[string]int64{},
	}

	// 1. 连通性与版本
	ver, err := src.ServerVersion(ctx)
	if err != nil {
		r.add(StatusFail, "源库连通性", "无法连接/查询失败: "+err.Error())
		return r, nil
	}
	r.ServerVersion = ver
	r.add(StatusOK, "源库连通性", "MySQL "+ver)

	// 2. 表完整性
	for _, t := range requiredTables {
		ok, err := src.TableExists(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("巡检表 %s 失败: %w", t, err)
		}
		if !ok {
			r.add(StatusFail, "表完整性", fmt.Sprintf("核心表 %s 不存在——疑似非 ZCard 1.x 库或库不完整", t))
		}
	}
	for _, t := range optionalTables {
		ok, err := src.TableExists(ctx, t)
		if err != nil {
			return nil, err
		}
		if !ok {
			r.add(StatusWarn, "表完整性", fmt.Sprintf("可选表 %s 不存在（将跳过对应数据）", t))
		}
	}
	if !r.HasFail() {
		r.add(StatusOK, "表完整性", fmt.Sprintf("核心表 %d 张齐全（可选表 %d 张）", len(requiredTables), len(optionalTables)))
	}

	// 3. schema 版本探测：「元→分」改造（1.x 2026_08_02 迁移）后的列必须是整型。
	//    停在改造前的老库金额差 100 倍，必须先升级 1.x 而不是在迁移工具里换算。
	for _, col := range [][2]string{
		{"user_groups", "min_recharge"}, {"user_groups", "min_consumption"},
		{"cards", "draft_premium"}, {"cards", "draft_cost"},
	} {
		dt, err := src.ColumnDataType(ctx, col[0], col[1])
		if err != nil {
			r.add(StatusWarn, "schema 版本探测", err.Error()+"（列可能不存在于更老版本，需先升级 1.x）")
			continue
		}
		if !integerTypes[dt] {
			r.add(StatusFail, "schema 版本探测",
				fmt.Sprintf("%s.%s 仍为 %s 类型——源库停在「元→分」改造（1.x 2026_08_02）之前，请先把 1.x 升级到 ≥ v1.9.x 再迁移",
					col[0], col[1], dt))
		}
	}
	if !r.HasFail() {
		r.add(StatusOK, "schema 版本探测", "金额列均为整型（分），schema 版本满足要求")
	}

	// 4. 密钥解析（显式 flag > 环境变量 > .env；卡密钥匙兜底走源库 settings）
	r.AppKeyRaw = firstNonEmpty(opt.AppKeyRaw, os.Getenv("ZCARD_V1_APP_KEY"), opt.Env["APP_KEY"])
	if r.AppKeyRaw != "" {
		key, err := laracrypt.ParseKey(r.AppKeyRaw)
		if err != nil {
			r.add(StatusFail, "APP_KEY 解析", err.Error())
		} else {
			r.AppKey = key
			r.add(StatusOK, "APP_KEY 解析", "已解析（32 字节）")
		}
	} else {
		r.add(StatusWarn, "APP_KEY 解析", "未提供 APP_KEY（--old-app-key / ZCARD_V1_APP_KEY / .env APP_KEY）；若源库存在加密凭据将无法迁移")
	}

	r.CardKeyRaw = firstNonEmpty(opt.CardKeyRaw, os.Getenv("ZCARD_V1_CARD_KEY"), opt.Env["CARD_ENCRYPTION_KEY"])
	cardKeyFrom := "flag/env/.env"
	if r.CardKeyRaw != "" {
		key, err := laracrypt.ParseKey(r.CardKeyRaw)
		if err != nil {
			r.add(StatusFail, "卡密钥匙解析", err.Error())
			r.CardKeyRaw = ""
		} else {
			r.CardKey = key
		}
	}
	// 兜底：源库 settings.card_encryption_key（值本身可能是 Crypt 密文或历史明文）
	if r.CardKey == nil {
		raw, found, err := src.SettingValue(ctx, "card_encryption_key")
		if err != nil {
			r.add(StatusWarn, "卡密钥匙解析", "读取 settings.card_encryption_key 失败: "+err.Error())
		} else if found {
			if r.AppKey == nil {
				r.add(StatusWarn, "卡密钥匙解析", "settings 中存有卡密钥匙但无 APP_KEY 可解密——请提供 --old-app-key 或改用 --old-card-key")
			} else {
				key, fromCipher, kerr := laracrypt.CardKeyFromSetting(raw, r.AppKey, !opt.SkipMAC)
				if kerr != nil {
					r.add(StatusFail, "卡密钥匙解析", "settings.card_encryption_key 无法解析: "+kerr.Error())
				} else {
					r.CardKey = key
					cardKeyFrom = "settings.card_encryption_key"
					if fromCipher {
						r.add(StatusOK, "卡密钥匙解析", "已从 settings 解出（Crypt 密文形态）")
					} else {
						r.add(StatusWarn, "卡密钥匙解析", "已从 settings 解出（历史明文形态，建议在 1.x 后台重存为密文）")
					}
				}
			}
		}
	}
	if r.CardKey != nil && cardKeyFrom == "flag/env/.env" {
		r.add(StatusOK, "卡密钥匙解析", "已解析（来源：flag/env/.env）")
	}

	// 5. 卡密抽样自检：形态统计 + （有密文时）解密验证
	contents, err := src.SampleStrings(ctx, "cards", "content", opt.SampleLimit)
	if err != nil {
		r.add(StatusWarn, "卡密抽样", "抽样失败: "+err.Error())
	} else {
		r.CardSampled = len(contents)
		for _, c := range contents {
			if !laracrypt.LooksEncrypted(c) {
				r.CardPlaintext++
				continue
			}
			r.CardEncrypted++
			if r.CardKey == nil {
				r.CardDecryptFailed++
				continue
			}
			if _, _, derr := laracrypt.OpenCard(c, r.CardKey, !opt.SkipMAC); derr != nil {
				r.CardDecryptFailed++
			}
		}
		switch {
		case r.CardSampled == 0:
			r.add(StatusInfo, "卡密抽样", "cards 表为空（或无样本）")
		case r.CardEncrypted == 0:
			r.add(StatusInfo, "卡密抽样", fmt.Sprintf("抽样 %d 条全部为明文形态（加密开关关闭期的存量）", r.CardSampled))
		case r.CardKey == nil:
			r.add(StatusFail, "卡密抽样", fmt.Sprintf("抽样 %d 条中 %d 条为密文，但卡密钥匙不可用——请提供 --old-card-key / .env CARD_ENCRYPTION_KEY 或可解的 APP_KEY",
				r.CardSampled, r.CardEncrypted))
		case r.CardDecryptFailed > 0:
			r.add(StatusFail, "卡密抽样", fmt.Sprintf("抽样 %d 条中 %d 条解密失败——疑似钥匙错配（MAC 拒收）或数据损坏",
				r.CardEncrypted, r.CardDecryptFailed))
		default:
			r.add(StatusOK, "卡密抽样", fmt.Sprintf("抽样 %d 条：密文 %d 全部解密成功，明文 %d", r.CardSampled, r.CardEncrypted, r.CardPlaintext))
		}
	}

	// 6. 凭据抽样自检（APP_KEY 系：supply credentials / payment config / settings SECRET）
	credChecks := []struct{ table, column string }{
		{"supply_sources", "credentials"},
		{"payment_channels", "config"},
	}
	credEncrypted := 0
	for _, cc := range credChecks {
		exists, err := src.TableExists(ctx, cc.table)
		if err != nil || !exists {
			continue
		}
		samples, err := src.SampleStrings(ctx, cc.table, cc.column, 2)
		if err != nil {
			continue // 列不存在等：表结构巡检已覆盖
		}
		for _, s := range samples {
			var inner string
			if cc.table == "payment_channels" {
				// 双层：列值是 JSON 字符串，内层才是密文
				if e := json.Unmarshal([]byte(s), &inner); e != nil {
					continue // 历史明文 JSON
				}
				s = inner
			}
			if !laracrypt.LooksEncrypted(s) {
				continue
			}
			credEncrypted++
			if r.AppKey == nil {
				r.add(StatusFail, "凭据抽样", fmt.Sprintf("%s.%s 存在密文但 APP_KEY 不可用", cc.table, cc.column))
				continue
			}
			c, _ := laracrypt.New(r.AppKey)
			if _, derr := c.OpenString(s); derr != nil {
				r.add(StatusFail, "凭据抽样", fmt.Sprintf("%s.%s 解密失败（钥匙错配?）: %v", cc.table, cc.column, derr))
			}
		}
	}
	// settings SECRET（mail_password 为代表）
	if raw, found, _ := src.SettingValue(ctx, "mail_password"); found {
		var inner string
		if e := json.Unmarshal([]byte(raw), &inner); e == nil && laracrypt.LooksEncrypted(inner) {
			credEncrypted++
			if r.AppKey == nil {
				r.add(StatusFail, "凭据抽样", "settings.mail_password 存在密文但 APP_KEY 不可用")
			} else if c, _ := laracrypt.New(r.AppKey); c != nil {
				if _, derr := c.OpenString(inner); derr != nil {
					r.add(StatusFail, "凭据抽样", "settings.mail_password 解密失败: "+derr.Error())
				}
			}
		}
	}
	if credEncrypted > 0 && !r.HasFail() {
		r.add(StatusOK, "凭据抽样", fmt.Sprintf("发现 %d 处密文凭据，抽样解密全部通过", credEncrypted))
	}
	if credEncrypted == 0 {
		r.add(StatusInfo, "凭据抽样", "未发现密文凭据（全为明文配置或表为空）")
	}

	// 7. 时区口径
	r.Timezone = opt.Env["APP_TIMEZONE"]
	if r.Timezone == "" {
		r.add(StatusInfo, "时区口径", "未提供 APP_TIMEZONE——时间列将按 UTC 处理；若 1.x 实际为 Asia/Shanghai 请在 .env 标注后重跑")
	} else {
		r.add(StatusOK, "时区口径", "APP_TIMEZONE="+r.Timezone)
	}

	// 8. 规模概览（信息级）
	for _, t := range []string{"users", "products", "cards", "orders", "payments", "bills"} {
		if n, err := src.CountRows(ctx, t); err == nil {
			r.TableCounts[t] = n
		}
	}

	// 9. 只读建议（常驻提示）
	r.add(StatusWarn, "安全建议", "迁移全程只读源库；生产切换请使用只读账号 + 提前归档两把密钥（钥匙丢失=卡密/凭据永久不可迁）")
	return r, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ErrPreflightFailed 巡检存在致命项（main 转为非零退出码）。
var ErrPreflightFailed = errors.New("preflight 未通过")
