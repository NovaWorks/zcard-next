package payment

// 渠道管理 + 回调管线 + 退款 v1（ 核心交易支付）。
//
// 渠道凭据 AES-256-GCM 加密存储（ZCARD_DATA_KEY），解密失败降级为空——列表绝不 500。
// 回调管线：四重校验（渠道/单号/金额/币种）→ 幂等三层 → markPaid → 事务后事件。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/currencyunit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/shopspring/decimal"
)

// PaymentRepoImpl 支付仓储。
type PaymentRepoImpl struct {
	data   *data.Data
	Cipher *crypto.Box // ZCARD_DATA_KEY
	reg    *Registry   // 渠道 adapter 注册表
	// 回调管线依赖（wire 注入， 破环点）：
	lifecycle orderport.OrderLifecycle    // 订单型回调 → 状态机推进 + order.paid 事件（同事务）
	wallet    walletport.Wallet           // 余额支付扣款 / 充值到账入账（通道 B 同事务）
	points    walletport.Points           // 充值赠送积分（积分账本；nil = 未装配跳过）
	outbox    events.Writer               // recharge.succeeded 等事件
	currency  settingsport.CurrencyReader // 币种快照换算（；nil = 同币直收）
	settings  settingsport.Provider       // site/url 等（ 回调地址拼接；nil = 相对路径）
	supplier  port.SupplierRecharger      // target=supply 充值入账（供货账户余额；nil = 未装配跳过）
}

// NewPaymentRepoImpl 构造。
func NewPaymentRepoImpl(d *data.Data, box *crypto.Box, reg *Registry, lifecycle orderport.OrderLifecycle, wallet walletport.Wallet, points walletport.Points, outbox events.Writer, currency settingsport.CurrencyReader, settings settingsport.Provider, supplier port.SupplierRecharger) *PaymentRepoImpl {
	return &PaymentRepoImpl{data: d, Cipher: box, reg: reg, lifecycle: lifecycle, wallet: wallet, points: points, outbox: outbox, currency: currency, settings: settings, supplier: supplier}
}

// ChargeSnapshot freezes the exchange rate and provider unit before any gateway request.
// Display precision never participates in charging or callback settlement.
type ChargeSnapshot struct {
	Units     int64
	Currency  string
	Rate      float64
	Precision int32
}

func chargeUnits(amount money.Cents, rate decimal.Decimal, unit currencyunit.ChargeUnit) (int64, error) {
	if amount <= 0 || !rate.IsPositive() || unit.Step <= 0 {
		return 0, fmt.Errorf("payment.INVALID_CHARGE_AMOUNT")
	}
	d := decimal.NewFromInt(int64(amount)).Shift(-2).Mul(rate).Shift(unit.Precision).
		Div(decimal.NewFromInt(unit.Step)).Round(0).Mul(decimal.NewFromInt(unit.Step))
	if !d.IsPositive() || d.GreaterThan(decimal.NewFromInt(1<<63-1)) {
		return 0, fmt.Errorf("payment.INVALID_CHARGE_AMOUNT: 换算金额过小或超出范围")
	}
	return d.IntPart(), nil
}

func (r *PaymentRepoImpl) computeCharge(ctx context.Context, driver string, cfg json.RawMessage, amount money.Cents) (ChargeSnapshot, error) {
	direct := ChargeSnapshot{}
	var probe struct {
		TargetCurrency string `json:"target_currency"`
		Currency       string `json:"currency"`
	}
	if err := json.Unmarshal(cfg, &probe); err != nil {
		return direct, fmt.Errorf("payment.CHANNEL_CONFIG_INVALID: %w", err)
	}
	tc := strings.ToUpper(strings.TrimSpace(probe.TargetCurrency))
	if driver == "epusdt" {
		fiat := strings.ToUpper(strings.TrimSpace(probe.Currency))
		if fiat == "" {
			fiat = "CNY"
		}
		if tc != "" && tc != fiat {
			return direct, fmt.Errorf("payment.CHANNEL_CONFIG_INVALID: GMPay 请使用 currency 配置法币")
		}
		tc = fiat
	}
	if tc == "" || tc == "CNY" {
		return direct, nil
	}
	unit, err := currencyunit.CurrencyChargeUnit(driver, tc)
	if err != nil {
		return direct, err
	}
	if r.currency == nil {
		return direct, fmt.Errorf("payment.CURRENCY_MISSING: 未配置币种 %s", tc)
	}
	rateStr, _, err := r.currency.CurrencyByCode(ctx, tc)
	if err != nil {
		return direct, fmt.Errorf("payment.CURRENCY_MISSING: 未配置币种 %s", tc)
	}
	rate, err := decimal.NewFromString(rateStr)
	if err != nil || !rate.IsPositive() {
		return direct, fmt.Errorf("payment.INVALID_EXCHANGE_RATE")
	}
	// Match the existing decimal(20,8) snapshot storage before doing arithmetic.
	rate = rate.Round(8)
	rf, _ := rate.Float64()
	if !rate.IsPositive() || rate.GreaterThan(decimal.RequireFromString("999999999999.99999999")) {
		return direct, fmt.Errorf("payment.INVALID_EXCHANGE_RATE")
	}
	// Use exactly the rate that will be recovered from the existing float field.
	rate = decimal.NewFromFloat(rf).Round(8)
	units, err := chargeUnits(amount, rate, unit)
	if err != nil {
		return direct, err
	}
	return ChargeSnapshot{Units: units, Currency: tc, Rate: rf, Precision: unit.Precision}, nil
}

func (r *PaymentRepoImpl) snapshotCharge(ctx context.Context, paymentID uint64, snap ChargeSnapshot) error {
	if snap.Units == 0 {
		return nil
	}
	_, err := data.Client(ctx, r.data).Payment.UpdateOneID(paymentID).
		SetChargedUnits(snap.Units).SetChargedCurrency(snap.Currency).
		SetExchangeRate(snap.Rate).SetChargedPrecision(snap.Precision).Save(ctx)
	return err
}

// callbackPrecision never consults current display settings. For legacy rows,
// infer only when the original authoritative amount/rate reproduces the exact
// stored provider units; otherwise retain the payment for manual review.
func (r *PaymentRepoImpl) callbackPrecision(ctx context.Context, p *ent.Payment) (int32, bool) {
	if p.ChargedPrecision >= 0 {
		return p.ChargedPrecision, p.ChargedPrecision <= 3
	}
	driver := r.snapshotDriver(ctx, p)
	unit, err := currencyunit.CurrencyChargeUnit(driver, p.ChargedCurrency)
	if err != nil {
		return -1, false
	}
	units, err := chargeUnits(money.Cents(p.Amount), decimal.NewFromFloat(p.ExchangeRate), unit)
	if err == nil && units == p.ChargedUnits {
		return unit.Precision, true
	}
	// Before the fix, PayPal strings/callbacks always used two decimals, even
	// for JPY/HUF/TWD. Recover that legacy scale only when the snapshot matches.
	if driver == "paypal" && unit.Precision == 0 {
		legacy := currencyunit.ChargeUnit{Precision: 2, Step: 100}
		units, err = chargeUnits(money.Cents(p.Amount), decimal.NewFromFloat(p.ExchangeRate), legacy)
		if err == nil && units == p.ChargedUnits {
			return 2, true
		}
	}
	return unit.Precision, false
}

func (r *PaymentRepoImpl) snapshotDriver(ctx context.Context, p *ent.Payment) string {
	if p.DriverSnapshot != "" {
		return p.DriverSnapshot
	}
	if ch, err := r.channelForPayment(ctx, p); err == nil {
		return ch.Driver
	}
	return ""
}

// ── 渠道管理（）────────────────────────────────────────────

// ListChannels 渠道列表（凭据脱敏）。
func (r *PaymentRepoImpl) ListChannels(ctx context.Context) ([]*ent.PaymentChannel, error) {
	return data.Client(ctx, r.data).PaymentChannel.Query().
		Where(paymentchannel.DeletedAtIsNil()).
		Order(ent.Asc(paymentchannel.FieldSort)).
		All(ctx)
}

// ChannelMethod 支付方式（收银台顾客看到的选项；params 承载网关路由参数）。
type ChannelMethod struct {
	ChannelUsage
	Code                 string            `json:"code"`
	Name                 string            `json:"name"`
	Icon                 string            `json:"icon,omitempty"`
	Enabled              bool              `json:"enabled"`
	Params               map[string]string `json:"params,omitempty"`
	Recommended          bool              `json:"recommended,omitempty"`
	RecommendLabel       string            `json:"recommend_label,omitempty"`
	RecommendDescription string            `json:"recommend_description,omitempty"`
}

// parseMethods 渠道方式列表解析（methods JSON 空 → nil = 单方式渠道旧语义）。
func parseMethods(ch *ent.PaymentChannel) []ChannelMethod {
	if len(ch.Methods) == 0 {
		return nil
	}
	b, err := json.Marshal(ch.Methods)
	if err != nil {
		return nil
	}
	var out []ChannelMethod
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// methodsJSON 校验并归一化方式列表 JSON（空串 → nil）。
func methodsJSON(raw string) ([]map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var ms []ChannelMethod
	if err := json.Unmarshal([]byte(raw), &ms); err != nil {
		return nil, fmt.Errorf("payment.METHODS_INVALID: 支付方式列表格式错误: %w", err)
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if err := validateRecommendation(m.RecommendLabel, m.RecommendDescription); err != nil {
			return nil, err
		}
		if seen[m.Code] {
			return nil, fmt.Errorf("payment.METHODS_INVALID: 支付方式标识不能重复")
		}
		seen[m.Code] = true
		if m.Code == "" || m.Name == "" {
			return nil, fmt.Errorf("payment.METHODS_INVALID: 方式的 code/name 必填")
		}
	}
	b, _ := json.Marshal(ms)
	var out []map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

// CreateChannel 创建渠道（凭据加密入库；methodsJSON=支付方式列表）。
func (r *PaymentRepoImpl) CreateChannel(ctx context.Context, name, code, driver, configJSON string, fee int64, feeType string, enabled bool, sort int32, icon string, methods []map[string]any, usage ...ChannelUsage) (*ent.PaymentChannel, error) {
	if driver == "bepusdt" && len(methods) > 0 {
		return nil, fmt.Errorf("payment.METHODS_INVALID: BEpusdt 请通过收款模式配置多链收银台，不支持本地支付方式列表")
	}
	// A deleted channel keeps its code for historical callbacks. A replacement
	// requested from the visible list must receive a fresh code and encryption AAD.
	if driver == "bepusdt" {
		c := data.Client(ctx, r.data)
		reserved, err := c.PaymentChannel.Query().Where(paymentchannel.SubsiteID(0), paymentchannel.Code(code), paymentchannel.DeletedAtNotNil()).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if reserved {
			base := code
			for n := 2; ; n++ {
				suffix := fmt.Sprintf("-%d", n)
				code = base[:min(len(base), 30-len(suffix))] + suffix
				exists, err := c.PaymentChannel.Query().Where(paymentchannel.SubsiteID(0), paymentchannel.Code(code)).Exist(ctx)
				if err != nil {
					return nil, err
				}
				if !exists {
					break
				}
			}
		}
	}
	enc, err := r.Cipher.Seal([]byte(configJSON), []byte("payment_channel:"+code))
	if err != nil {
		return nil, fmt.Errorf("payment: 凭据加密失败: %w", err)
	}
	q := data.Client(ctx, r.data).PaymentChannel.Create().
		SetName(name).
		SetCode(code).
		SetDriver(driver).
		SetConfig(enc).
		SetFee(fee).
		SetFeeType(paymentchannel.FeeType(feeType)).
		SetEnabled(enabled).
		SetSort(sort).
		SetIcon(icon)
	if len(usage) > 0 {
		q.SetNillableAllowPurchase(usage[0].AllowPurchase).SetNillableAllowMemberRecharge(usage[0].AllowMemberRecharge).SetNillableAllowSupplyRecharge(usage[0].AllowSupplyRecharge)
	}
	if methods != nil {
		q = q.SetMethods(methods)
	}
	return q.Save(ctx)
}

// UpdateChannel 更新渠道（config_json=**** 跳过凭据修改；feeType 空=不修改；
// setIcon/setMethods=false 保持原值——proto optional 语义）。
func (r *PaymentRepoImpl) UpdateChannel(ctx context.Context, id uint64, name, configJSON string, fee int64, feeType string, enabled bool, sort int32, setIcon bool, icon string, setMethods bool, methods []map[string]any, usage ...ChannelUsage) (*ent.PaymentChannel, error) {
	var result *ent.PaymentChannel
	err := data.Tx(ctx, r.data, func(txCtx context.Context) error {
		ch, err := data.Client(txCtx, r.data).PaymentChannel.UpdateOneID(id).AddSort(0).Save(txCtx)
		if err != nil {
			return err
		}
		if !ch.DeletedAt.IsZero() {
			return fmt.Errorf("payment.CHANNEL_DELETED: 渠道已删除，请新建渠道")
		}
		result, err = r.updateChannel(txCtx, id, name, configJSON, fee, feeType, enabled, sort, setIcon, icon, setMethods, methods, usage...)
		return err
	})
	return result, err
}

func (r *PaymentRepoImpl) updateChannel(ctx context.Context, id uint64, name, configJSON string, fee int64, feeType string, enabled bool, sort int32, setIcon bool, icon string, setMethods bool, methods []map[string]any, usage ...ChannelUsage) (*ent.PaymentChannel, error) {
	if setMethods && len(methods) > 0 {
		ch, err := data.Client(ctx, r.data).PaymentChannel.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if ch.Driver == "bepusdt" {
			return nil, fmt.Errorf("payment.METHODS_INVALID: BEpusdt 请通过收款模式配置多链收银台，不支持本地支付方式列表")
		}
	}
	q := data.Client(ctx, r.data).PaymentChannel.UpdateOneID(id)
	if len(usage) > 0 {
		q.SetNillableAllowPurchase(usage[0].AllowPurchase).SetNillableAllowMemberRecharge(usage[0].AllowMemberRecharge).SetNillableAllowSupplyRecharge(usage[0].AllowSupplyRecharge)
	}
	if name != "" {
		q.SetName(name)
	}
	if configJSON != "" && configJSON != `"****"` {
		ch, err := data.Client(ctx, r.data).PaymentChannel.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := r.checkBepusdtConfigChange(ctx, ch, configJSON); err != nil {
			return nil, err
		}
		enc, err := r.Cipher.Seal([]byte(configJSON), []byte("payment_channel:"+ch.Code))
		if err != nil {
			return nil, err
		}
		q.SetConfig(enc)
	}
	if fee >= 0 {
		q.SetFee(fee)
	}
	if feeType != "" {
		q.SetFeeType(paymentchannel.FeeType(feeType))
	}
	q.SetEnabled(enabled)
	if sort >= 0 {
		q.SetSort(sort)
	}
	if setIcon {
		q.SetIcon(icon)
	}
	if setMethods {
		if methods != nil {
			q.SetMethods(methods)
		} else {
			q.ClearMethods()
		}
	}
	return q.Save(ctx)
}

// DeleteChannel 删除渠道。
func (r *PaymentRepoImpl) DeleteChannel(ctx context.Context, id uint64) error {
	return data.Tx(ctx, r.data, func(txCtx context.Context) error {
		c := data.Client(txCtx, r.data)
		ch, err := c.PaymentChannel.UpdateOneID(id).AddSort(0).Save(txCtx)
		if err != nil {
			return err
		}
		if ch.Driver == "bepusdt" {
			if !ch.DeletedAt.IsZero() {
				return nil
			}
			if ch.Enabled {
				return fmt.Errorf("payment.CHANNEL_ENABLED: 请先停用渠道再删除")
			}
			// Keep the original identity and encrypted credentials for late callbacks.
			_, err = c.PaymentChannel.UpdateOneID(id).SetDeletedAt(time.Now().UTC()).Save(txCtx)
			return err
		}
		return c.PaymentChannel.DeleteOneID(id).Exec(txCtx)
	})
}

// DecryptConfig 解密凭据（失败降级为空，铁律 5）。
func (r *PaymentRepoImpl) DecryptConfig(ch *ent.PaymentChannel) json.RawMessage {
	if r.Cipher == nil {
		return json.RawMessage("{}")
	}
	plain, err := r.Cipher.Open(ch.Config, []byte("payment_channel:"+ch.Code))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(plain)
}

// CallbackURL 渠道回调地址（站点 URL 拼接；site/url 未配置回落相对路径——
// ：admin 配置面板展示复制，填到支付平台 webhook/notify 配置）。
func (r *PaymentRepoImpl) CallbackURL(ctx context.Context, code string) string {
	base := ""
	if r.settings != nil {
		if raw, err := r.settings.Get(ctx, "site", "url"); err == nil && len(raw) > 2 {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				base = strings.TrimRight(strings.TrimSpace(v), "/")
				// 站点设置允许裸域名；先补协议，避免 absolutePayURL 将域名
				// 当作相对路径再次拼接到请求 Host 后。
				if strings.HasPrefix(base, "//") {
					base = "https:" + base
				} else if base != "" && !strings.Contains(base, "://") {
					base = "https://" + base
				}
			}
		}
	}
	return base + "/payments/callback/" + code
}

// ConfiguredFields 已配置字段名列表（解密后统计非空值——仅名不显值，脱敏；
// admin 前端「已配置」状态判定 + 敏感字段编辑时留空不覆盖）。
func (r *PaymentRepoImpl) ConfiguredFields(ch *ent.PaymentChannel) []string {
	cfg := r.DecryptConfig(ch)
	if len(cfg) == 0 || string(cfg) == "{}" {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(cfg, &m) != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		var sv string
		if json.Unmarshal(v, &sv) == nil {
			if strings.TrimSpace(sv) != "" {
				out = append(out, k)
			}
			continue
		}
		// 数组值（多选字段 token/network）：非空数组算已配置
		var arr []json.RawMessage
		if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ── 支付单（）────────────────────────────────────────────

// CreatePayment 创建支付单。
func (r *PaymentRepoImpl) CreatePayment(ctx context.Context, orderID uint64, channel string, amount int64, idemKey string, methods ...string) (*ent.Payment, error) {
	var result *ent.Payment
	err := data.Tx(ctx, r.data, func(ctx context.Context) error {
		client := data.Client(ctx, r.data)
		o, err := client.Order.Get(ctx, orderID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if o.Status != order.StatusPendingPayment || o.ExpiredAt.IsZero() || !now.Before(o.ExpiredAt) || o.ExpiryReview {
			return fmt.Errorf("payment.ORDER_EXPIRED: 订单已过期或待核对，请重新下单")
		}
		ch, err := resolveChannel(ctx, r.data, o.SubsiteID, channel)
		if err != nil {
			return err
		}
		if !ch.Enabled {
			return fmt.Errorf("payment.CHANNEL_DISABLED")
		}
		// Serialize attempt creation against cancellation/settlement without keeping a
		// transaction open during the external gateway request.
		n, err := client.Order.Update().Where(order.ID(o.ID), order.StatusEQ(order.StatusPendingPayment), order.VersionEQ(o.Version), order.ExpiredAtGT(now)).SetVersion(o.Version + 1).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("payment.ORDER_CHANGED")
		}
		method := ""
		if len(methods) > 0 {
			method = methods[0]
		}
		price, err := r.price(ctx, ch, o.TotalAmount, method)
		if err != nil {
			return err
		}
		if err = checkQuote(ctx, pricingQuote(price, ch.Code, ch.ID, o.OrderNo, 0)); err != nil {
			return err
		}
		if port.QuoteKey(ctx) != "" {
			candidates, e := client.Payment.Query().Where(payment.OrderID(o.ID), payment.ChannelID(ch.ID), payment.StatusEQ(payment.StatusPending)).Order(ent.Desc(payment.FieldID)).All(ctx)
			if e != nil {
				return e
			}
			for _, prev := range candidates {
				if prev.GatewayOrderRef != "" && len(prev.PricingSnapshot) > 0 && pricingOf(prev) == price {
					result = prev
					return nil
				}
			}
		}
		ref, err := bepusdtNonce()
		if err != nil {
			return err
		}
		result, err = client.Payment.Create().SetPricingSnapshot(pricingJSON(price)).SetFee(price.Fee).SetGatewayOrderRef("ZP" + ref[:30]).SetOrderID(orderID).SetSubsiteID(o.SubsiteID).SetChannel(channel).SetChannelID(ch.ID).SetDriverSnapshot(ch.Driver).SetExpiresAt(o.ExpiredAt).SetAmount(price.Total).SetChargedUnits(price.Charge.Units).SetChargedCurrency(price.Charge.Currency).SetChargedPrecision(price.Charge.Precision).SetExchangeRate(price.Charge.Rate).SetStatus(payment.StatusPending).SetIdempotencyKey(idemKey).Save(ctx)
		return err
	})
	return result, err
}

// CreateRechargePayment 充值支付单（RechargePayer 端口实现）：
// 建单（关联 recharge_order_id）→ 渠道发起（金额=服务端落库值）→ 返回跳转信息。
func (r *PaymentRepoImpl) CreateRechargePayment(ctx context.Context, rechargeOrderID uint64, channel, method string, amount money.Cents) (*port.RechargePaymentInfo, error) {
	client := data.Client(ctx, r.data)
	ro, err := client.RechargeOrder.Get(ctx, rechargeOrderID)
	if err != nil {
		return nil, fmt.Errorf("payment.RECHARGE_NOT_FOUND")
	}
	ch, err := resolveChannel(ctx, r.data, 0, channel)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("payment.CHANNEL_NOT_FOUND")
	}
	if err != nil {
		return nil, err
	}
	if !ch.Enabled {
		return nil, fmt.Errorf("payment.CHANNEL_DISABLED")
	}
	if ch.Driver == "wallet" {
		return nil, fmt.Errorf("payment.RECHARGE_CHANNEL_INVALID: 充值不支持余额渠道")
	}
	scene := sceneMemberRecharge
	if ro.Target == rechargeorder.TargetSupply {
		scene = sceneSupplyRecharge
	}
	if err := checkPaymentUsage(ch, method, scene); err != nil {
		return nil, err
	}
	// 方式级路由（与订单支付同口径）：多方式渠道 method 必填且须在启用列表内
	var methodCode string
	var methodParams map[string]string
	if ms := parseMethods(ch); len(ms) > 0 {
		for _, m := range ms {
			if m.Enabled && m.Code == method {
				methodCode, methodParams = m.Code, m.Params
				break
			}
		}
		if methodCode == "" {
			return nil, fmt.Errorf("payment.METHOD_INVALID: 请选择该渠道支持的支付方式")
		}
	}
	if ch.Driver == "bepusdt" {
		return r.createBepusdtPayment(ctx, ch.ID, 0, ro.ID, method)
	}
	price, err := r.price(ctx, ch, ro.Amount, method)
	if err != nil {
		return nil, err
	}
	if err = checkQuote(ctx, pricingQuote(price, ch.Code, ch.ID, scene, 0)); err != nil {
		return nil, err
	}
	ref, err := bepusdtNonce()
	if err != nil {
		return nil, err
	}
	p, err := client.Payment.Create().SetPricingSnapshot(pricingJSON(price)).SetFee(price.Fee).SetGatewayOrderRef("ZP" + ref[:30]).
		SetChargedUnits(price.Charge.Units).SetChargedCurrency(price.Charge.Currency).SetChargedPrecision(price.Charge.Precision).SetExchangeRate(price.Charge.Rate).
		SetRechargeOrderID(ro.ID).
		SetSubsiteID(0).SetChannelID(ch.ID).SetDriverSnapshot(ch.Driver).
		SetChannel(channel).
		SetAmount(price.Total).
		SetStatus(payment.StatusPending).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	provider, err := r.reg.Provider(ch.Driver)
	if err != nil {
		return nil, fmt.Errorf("payment.CHANNEL_UNSUPPORTED: %w", err)
	}
	cfg := r.DecryptConfig(ch)
	if err := provider.ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("payment.CHANNEL_CONFIG_INVALID: %w", err)
	}
	snap := price.Charge
	// 回跳/回调绝对化（同订单支付口径）：return 回充值 tab——曾不传，
	// 网关回跳空地址即 404；notify 走 site/url 或请求 Host
	info, err := r.dispatchPayment(ctx, p, provider, port.CreatePaymentRequest{
		OrderNo: p.GatewayOrderRef, GatewayOrderRef: p.GatewayOrderRef,
		Channel:      channel,
		Amount:       money.Cents(p.Amount),
		Subject:      "余额充值",
		ChargedUnits: snap.Units, ChargedCurrency: snap.Currency,
		ReturnURL:     absolutePayURL(ctx, "/member?tab=recharge"),
		NotifyBaseURL: absolutePayURL(ctx, r.callbackURLFor(ctx, ch)),
		Config:        cfg,
		MethodCode:    methodCode, MethodParams: methodParams,
	})
	if err != nil {
		return nil, fmt.Errorf("payment.CREATE_FAILED: %w", err)
	}
	return &port.RechargePaymentInfo{
		PaymentID: p.ID, Type: info.Type, Payload: string(info.Payload), BaseCents: price.Base, FeeCents: price.Fee, TotalCents: price.Total,
	}, nil
}

// GetPayment 按 ID 查支付单。
func (r *PaymentRepoImpl) GetPayment(ctx context.Context, id uint64) (*ent.Payment, error) {
	return data.Client(ctx, r.data).Payment.Get(ctx, id)
}

// ListPayments 支付单列表。
func (r *PaymentRepoImpl) ListPayments(ctx context.Context, status, orderNo string, cursor uint64, limit int32, reviewOnly ...bool) ([]*ent.Payment, error) {
	q := data.Client(ctx, r.data).Payment.Query().
		Where(payment.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).
		Order(ent.Desc(payment.FieldID)).
		Limit(int(limit))
	if len(reviewOnly) > 0 && reviewOnly[0] {
		q = q.Where(payment.ReviewReasonNEQ(""))
	}
	if status != "" {
		q = q.Where(payment.StatusEQ(payment.Status(status)))
	}
	if cursor > 0 {
		q = q.Where(payment.IDLT(cursor))
	}
	if orderNo != "" {
		o, err := data.Client(ctx, r.data).Order.Query().Where(order.OrderNo(orderNo)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		q = q.Where(payment.OrderID(o.ID))
	}
	return q.All(ctx)
}

// HandleCallback 回调处理管线（ 核心——四重校验+幂等+业务推进）。
// 事务内：行锁 payment → 幂等三层 → 按支付单类型分流：
//
//	订单型（order_id>0）：余额渠道先扣款（wallet.DebitInTx，同事务）→
//
// OrderLifecycle.MarkPaid（状态机 CAS + 状态事件 + outbox order.paid， 破环点）
//
//	充值型（recharge_order_id>0）：充值单 pending→success → 余额入账
//
// （amount+gift，reference=recharge:<paymentID> 幂等）→ outbox recharge.succeeded
//
// 支付确认前零入账（铁律 16）；事件与入账同事务，回滚不残留。
func (r *PaymentRepoImpl) HandleCallback(ctx context.Context, paymentID uint64, fact CallbackFact) error {
	if !fact.Success {
		return fmt.Errorf("payment.NOT_SUCCESSFUL")
	}
	return data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)

		// 1) 行锁支付单
		p, err := client.Payment.Query().Where(payment.ID(paymentID)).Only(txCtx)
		if ent.IsNotFound(err) {
			return fmt.Errorf("payment.NOT_FOUND")
		}
		if err != nil {
			return err
		}

		// Native BEpusdt must bind the exact attempt even for duplicate callbacks.
		if p.DriverSnapshot == "bepusdt" {
			p, err = r.lockBepusdtPayment(txCtx, p.ID)
			if err != nil {
				return err
			}
			if fact.GatewayOrderRef == "" || fact.GatewayOrderRef != p.GatewayOrderRef || fact.ChannelOrderNo == "" || (p.ChannelOrderNo != "" && p.ChannelOrderNo != fact.ChannelOrderNo) {
				return fmt.Errorf("payment.GATEWAY_ORDER_MISMATCH")
			}
			if fact.Channel != p.Channel || fact.Amount != p.Amount || fact.Currency != "CNY" {
				return fmt.Errorf("payment.AMOUNT_MISMATCH")
			}
		}

		// 2) 幂等第一层：已 success 直接 ACK
		if p.Status == payment.StatusSuccess {
			return nil
		}

		// 3) 四重校验（渠道/单号/金额/币种——金额永远对服务端权威值）
		// 跨币路径（ 快照）：回调金额=渠道币种最小单位，对 charged_units
		// 精确核对（网关回显下单金额，零二次换算）；实收换算回基础货币分落 charged_amount。
		// 同币路径（charged_units=0）：旧口径 fact.Amount==amount + CNY。
		if fact.Channel != p.Channel {
			return fmt.Errorf("payment.CHANNEL_MISMATCH")
		}
		chargedBase := fact.Amount
		reviewReason := ""
		precision := p.ChargedPrecision
		if p.ChargedUnits > 0 {
			// Old PayPal zero-decimal callback parsing used x100 units. Preserve
			// the historical snapshot representation; new payments use protocol units.
			if p.ChargedPrecision < 0 && r.snapshotDriver(txCtx, p) == "paypal" {
				unit, err := currencyunit.CurrencyChargeUnit("paypal", p.ChargedCurrency)
				if err == nil && unit.Precision == 0 && fact.Amount > 0 && fact.Amount <= (1<<63-1)/100 && fact.Amount*100 == p.ChargedUnits {
					fact.Amount *= 100
				}
			}
			if fact.Amount != p.ChargedUnits {
				return fmt.Errorf("payment.AMOUNT_MISMATCH: want units %d got %d", p.ChargedUnits, fact.Amount)
			}
			if !strings.EqualFold(fact.Currency, p.ChargedCurrency) {
				return fmt.Errorf("payment.CURRENCY_MISMATCH: want %s got %s", p.ChargedCurrency, fact.Currency)
			}
			var trustworthy bool
			precision, trustworthy = r.callbackPrecision(txCtx, p)
			rate := decimal.NewFromFloat(p.ExchangeRate)
			if !trustworthy || !rate.IsPositive() {
				// Record receipt without crediting wallet or fulfilling an ambiguous payment.
				reviewReason = "历史跨币支付金额单位无法确认，请核对网关实收后补单或退款"
				chargedBase = 0
			} else {
				base, _ := money.FromDisplay(fact.Amount, rate, precision)
				chargedBase = int64(base)
			}
		} else {
			if fact.Amount != p.Amount {
				return fmt.Errorf("payment.AMOUNT_MISMATCH: want %d got %d", p.Amount, fact.Amount)
			}
			if fact.Currency != "CNY" {
				return fmt.Errorf("payment.CURRENCY_MISMATCH")
			}
		}

		if p.OrderID > 0 {
			o, err := client.Order.Get(txCtx, p.OrderID)
			if err != nil {
				return err
			}
			if fact.OrderNo != "" && fact.OrderNo != o.OrderNo && fact.OrderNo != p.GatewayOrderRef {
				return fmt.Errorf("payment.ORDER_MISMATCH")
			}
			fact.OrderNo = o.OrderNo
		}
		// 4) 幂等第二层 + 分流推进（支付单 CAS pending→success）
		now := time.Now().UTC()
		affected, err := client.Payment.Update().
			Where(payment.ID(p.ID), payment.StatusNEQ(payment.StatusSuccess)).
			SetStatus(payment.StatusSuccess).
			SetChargedAmount(chargedBase).
			SetChargedPrecision(precision).
			SetReviewReason(reviewReason).
			SetChannelOrderNo(fact.ChannelOrderNo).
			SetPaidAt(now).
			SetRaw(fact.Raw).
			Save(txCtx)
		if err != nil {
			return err
		}

		if affected != 1 {
			return fmt.Errorf("payment.CONCURRENT_UPDATE")
		}
		if reviewReason != "" {
			return nil
		}
		if p.OrderID > 0 {
			o, err := client.Order.Get(txCtx, p.OrderID)
			if err != nil {
				return err
			}
			if o.Status != order.StatusPendingPayment {
				ch, err := r.channelForPayment(txCtx, p)
				if err != nil {
					return err
				}
				if ch.Driver == "wallet" {
					return fmt.Errorf("payment.ORDER_NOT_PENDING")
				}
				reason := "订单已关闭或已有付款，到账待核对，请处理补单或退款"
				if _, err = client.Payment.UpdateOneID(p.ID).SetReviewReason(reason).Save(txCtx); err != nil {
					return err
				}
				_, err = client.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus(string(o.Status)).SetEvent("payment_review").SetOperator("system").SetReason(reason).Save(txCtx)
				return err
			}
		}
		if p.RechargeOrderID > 0 {
			return r.settleRecharge(txCtx, p, fact)
		}
		if p.OrderID > 0 {
			// 钱包直付等内部路径 fact 不带单号——按支付单回填（MarkPaid 判据）
			if o, err := client.Order.Get(txCtx, p.OrderID); err == nil {
				fact.OrderNo = o.OrderNo
			}
		}
		return r.settleOrder(txCtx, p, fact)
	})
}

// settleOrder 订单型推进：余额渠道扣款 → OrderLifecycle.MarkPaid（同事务）。
func (r *PaymentRepoImpl) settleOrder(ctx context.Context, p *ent.Payment, fact CallbackFact) error {
	// 余额支付：先扣款（幂等键 order_pay:<orderID>；余额不足整事务回滚）
	ch, err := r.channelForPayment(ctx, p)
	if err != nil {
		return err
	}
	if r.wallet != nil && ch.Driver == "wallet" && p.OrderID > 0 {
		o, err := data.Client(ctx, r.data).Order.Get(ctx, p.OrderID)
		if err != nil {
			return err
		}
		if err := r.wallet.DebitInTx(ctx, walletport.Entry{
			UserID: o.UserID, Direction: walletport.DirectionOut,
			Type: "order_pay", Amount: money.Cents(p.Amount),
			Reference: fmt.Sprintf("order_pay:%d", p.OrderID),
			OrderID:   p.OrderID,
		}); err != nil {
			return fmt.Errorf("payment.BALANCE_INSUFFICIENT: %w", err)
		}
	}
	// 订单置 paid（状态机 CAS + 事件 + outbox order.paid；幂等：已 paid 直接成功）
	if r.lifecycle == nil || p.OrderID == 0 {
		return fmt.Errorf("payment.ORDER_LIFECYCLE_UNBOUND")
	}
	return r.lifecycle.MarkPaid(ctx, orderport.PaidFact{
		OrderNo:        fact.OrderNo,
		PaymentID:      p.ID,
		Channel:        p.Channel,
		Amount:         money.Cents(p.Amount),
		ChannelOrderNo: fact.ChannelOrderNo,
	})
}

// settleRecharge 充值型推进：充值单 success → 余额入账（幂等键 recharge:<paymentID>）
// → outbox recharge.succeeded。金额与赠送全部取服务端落库值（铁律 16）。
func (r *PaymentRepoImpl) settleRecharge(ctx context.Context, p *ent.Payment, fact CallbackFact) error {
	client := data.Client(ctx, r.data)
	var ro *ent.RechargeOrder
	var err error
	if p.DriverSnapshot == "bepusdt" {
		if r.data.Dialect != db.SQLite {
			ro, err = client.RechargeOrder.Query().Where(rechargeorder.ID(p.RechargeOrderID)).ForUpdate().Only(ctx)
		} else {
			ro, err = client.RechargeOrder.UpdateOneID(p.RechargeOrderID).AddAmount(0).Save(ctx)
		}
	} else {
		ro, err = client.RechargeOrder.Get(ctx, p.RechargeOrderID)
	}
	if err != nil {
		return err
	}
	reviewOrReject := func() error {
		if p.DriverSnapshot == "bepusdt" {
			_, err := client.Payment.UpdateOneID(p.ID).SetReviewReason("充值单已处理，本次到账待核对，请处理退款").Save(ctx)
			return err
		}
		return fmt.Errorf("payment.RECHARGE_NOT_PENDING")
	}
	if ro.Status != rechargeorder.StatusPending {
		return reviewOrReject()
	}
	// A second channel can pay the same recharge concurrently. The status CAS
	// claims settlement across channels before either wallet or supplier credit.
	affected, err := client.RechargeOrder.Update().
		Where(rechargeorder.ID(ro.ID), rechargeorder.StatusEQ(rechargeorder.StatusPending)).
		SetStatus(rechargeorder.StatusSuccess).SetPaymentID(p.ID).
		SetPaidAt(time.Now().UTC()).Save(ctx)
	if err != nil {
		return err
	}
	if affected != 1 {
		return reviewOrReject()
	}
	// 入账分支（target 由建单时定；金额全部取服务端落库值，铁律 16）：
	// balance → 用户钱包余额（本金+赠送）+ 赠送积分；
	// supply → 对接账户供货余额（本金；供货预存无赠送）。
	total := ro.Amount + ro.GiftAmount
	if ro.Target == rechargeorder.TargetSupply {
		if ro.SupplierAccountID > 0 && r.supplier != nil {
			if err := r.supplier.Recharge(ctx, ro.SupplierAccountID, total,
				fmt.Sprintf("recharge:%d", p.ID),
				fmt.Sprintf("对接账户自助充值到账（用户 #%d，本金 %d 分 + 赠送 %d 分）", ro.UserID, ro.Amount, ro.GiftAmount)); err != nil {
				return fmt.Errorf("payment.SUPPLY_RECHARGE_FAILED: %w", err)
			}
		}
	} else if total > 0 && r.wallet != nil {
		if err := r.wallet.CreditInTx(ctx, walletport.Entry{
			UserID: ro.UserID, Direction: walletport.DirectionIn,
			Type: "recharge", Amount: money.Cents(total),
			Reference: fmt.Sprintf("recharge:%d", p.ID),
			OrderID:   0,
			Remark:    fmt.Sprintf("充值到账（本金 %d 分 + 赠送 %d 分）", ro.Amount, ro.GiftAmount),
		}); err != nil {
			return fmt.Errorf("payment.RECHARGE_CREDIT_FAILED: %w", err)
		}
	}
	// 赠送积分（幂等键 points:recharge:<paymentID>；积分账本 ）
	if ro.GiftPoints > 0 && r.points != nil {
		if err := r.points.PointCreditInTx(ctx, walletport.PointEntry{
			UserID: ro.UserID, Direction: "in", Type: "earn_recharge",
			Amount: int64(ro.GiftPoints), Reference: fmt.Sprintf("points:recharge:%d", p.ID),
			Remark: fmt.Sprintf("充值赠送积分 %d", ro.GiftPoints),
		}); err != nil {
			return fmt.Errorf("payment.RECHARGE_POINTS_FAILED: %w", err)
		}
	}
	// 充值成功事件（notify 消费）
	if r.outbox != nil {
		payload, _ := json.Marshal(map[string]any{
			"recharge_id": ro.ID, "payment_id": p.ID, "user_id": ro.UserID,
			"amount": ro.Amount, "gift_amount": ro.GiftAmount, "total": total,
		})
		_ = r.outbox.Write(ctx, "payment", events.RechargeSucceeded,
			fmt.Sprintf("recharge:%d", ro.ID), fmt.Sprintf("recharge:%d:succeeded", ro.ID), payload)
	}
	return nil
}

// isWalletChannel 渠道驱动是否为余额支付（wallet driver）。
func (r *PaymentRepoImpl) isWalletChannel(ctx context.Context, code string) bool {
	ch, err := data.Client(ctx, r.data).PaymentChannel.Query().
		Where(paymentchannel.Code(code)).Only(ctx)
	return err == nil && ch.Driver == "wallet"
}

// CallbackFact 回调事实（适配器产出）。
type CallbackFact struct {
	GatewayOrderRef string
	Channel         string
	ChannelOrderNo  string
	OrderNo         string
	Amount          int64 // 分（基础货币）
	Currency        string
	Success         bool
	Raw             json.RawMessage
}

// ── 退款（）───────────────────────────────────────────────

// CreateRefund 创建退款单。
func (r *PaymentRepoImpl) CreateRefund(ctx context.Context, orderID uint64, amount int64, channel, reason string) (*ent.RefundOrder, error) {
	return data.Client(ctx, r.data).RefundOrder.Create().
		SetOrderID(orderID).
		SetAmount(amount).
		SetChannel(refundorder.Channel(channel)).
		SetReason(reason).
		SetStatus(refundorder.StatusCreated).
		Save(ctx)
}

// ListRefunds 退款单列表。
func (r *PaymentRepoImpl) ListRefunds(ctx context.Context, status string) ([]*ent.RefundOrder, error) {
	q := data.Client(ctx, r.data).RefundOrder.Query().
		Order(ent.Desc(refundorder.FieldCreatedAt)).
		Limit(50)
	if status != "" {
		q = q.Where(refundorder.StatusEQ(refundorder.Status(status)))
	}
	return q.All(ctx)
}

// ── DTO 转换 ────────────────────────────────────────────────

// ToChannelPB 转 admin 协议（凭据脱敏；icon/methods_json 原样下发）。
func ToChannelPB(ch *ent.PaymentChannel) *adminv1.Channel {
	pb := &adminv1.Channel{
		Id: ch.ID, Name: paymentChannelName(ch), Code: ch.Code, Driver: ch.Driver,
		ConfigJson: `"****"`, // 凭据永不明文下发
		Fee:        ch.Fee, FeeType: string(ch.FeeType), FeeBearer: string(ch.FeeBearer), Recommended: ch.Recommended, RecommendLabel: ch.RecommendLabel, RecommendDescription: ch.RecommendDescription,
		Enabled: ch.Enabled, Sort: ch.Sort,
		Icon:          ch.Icon,
		AllowPurchase: &ch.AllowPurchase, AllowMemberRecharge: &ch.AllowMemberRecharge, AllowSupplyRecharge: &ch.AllowSupplyRecharge,
	}
	if ms := parseMethods(ch); len(ms) > 0 {
		if b, err := json.Marshal(ms); err == nil {
			pb.MethodsJson = string(b)
		}
	}
	return pb
}

// ToPaymentPB 转支付单协议。
func ToPaymentPB(p *ent.Payment, orderNo string) *adminv1.Payment {
	out := &adminv1.Payment{
		Id: p.ID, OrderId: p.OrderID, OrderNo: orderNo,
		Channel: p.Channel, ChannelOrderNo: p.ChannelOrderNo,
		AmountCents: p.Amount, ChargedCents: p.ChargedAmount, FeeCents: p.Fee,
		Status: string(p.Status),
	}
	out.ReviewReason = p.ReviewReason
	out.DriverSnapshot = p.DriverSnapshot
	if !p.ExpiresAt.IsZero() {
		out.ExpiresAt = p.ExpiresAt.Unix()
	}
	if !p.PaidAt.IsZero() {
		out.PaidAt = p.PaidAt.Unix()
	}
	if !p.CreatedAt.IsZero() {
		out.CreatedAt = p.CreatedAt.Unix()
	}
	return out
}

// ToRefundPB 转退款单协议。
func ToRefundPB(rf *ent.RefundOrder, orderNo string) *adminv1.RefundOrder {
	return &adminv1.RefundOrder{
		Id: rf.ID, OrderId: rf.OrderID, OrderNo: orderNo,
		AmountCents: rf.Amount, FeeCents: rf.FeeAmount, Channel: string(rf.Channel),
		Status: string(rf.Status), Reason: rf.Reason,
		UpstreamRefundId: rf.UpstreamRefundID,
	}
}

// RefundOrder is used by procurement. There is no automatic gateway refund executor.
// Return an error so procurement transfers the order to manual review instead of
// creating a receipt that can never settle (especially for guest and mixed orders).
func (r *PaymentRepoImpl) RefundOrder(ctx context.Context, orderID uint64, amount money.Cents, reason string) error {
	return fmt.Errorf("payment: 自动退款尚未执行，请在订单详情核实后退款或补发")
}

// Normalize only generated legacy names; preserve operator names and all identities.
func paymentChannelName(ch *ent.PaymentChannel) string {
	if ch.Driver != "epusdt" {
		return ch.Name
	}
	const old = "EPUSDT / GM Pay（多链多币种）"
	if ch.Name == old {
		return "GM Pay"
	}
	if suffix, ok := strings.CutPrefix(ch.Name, old+" "); ok {
		if n, err := strconv.Atoi(suffix); err == nil && n >= 2 && strconv.Itoa(n) == suffix {
			return "GM Pay " + suffix
		}
	}
	return ch.Name
}
