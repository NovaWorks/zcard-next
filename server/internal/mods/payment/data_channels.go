package payment

// T1 渠道管理 + T4 回调管线 + T6 退款 v1（P1-04 核心交易支付）。
//
// 渠道凭据 AES-256-GCM 加密存储（ZCARD_DATA_KEY），解密失败降级为空——列表绝不 500。
// 回调管线：四重校验（渠道/单号/金额/币种）→ 幂等三层 → markPaid → 事务后事件。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

// PaymentRepoImpl 支付仓储。
type PaymentRepoImpl struct {
	data   *data.Data
	Cipher *crypto.Box // ZCARD_DATA_KEY
	reg    *Registry   // 渠道 adapter 注册表
	// M1b 回调管线依赖（wire 注入，§4.6 破环点）：
	lifecycle orderport.OrderLifecycle    // 订单型回调 → 状态机推进 + order.paid 事件（同事务）
	wallet    walletport.Wallet           // 余额支付扣款 / 充值到账入账（通道 B 同事务）
	points    walletport.Points           // 充值赠送积分（积分账本；nil = 未装配跳过）
	outbox    events.Writer               // recharge.succeeded 等事件
	currency  settingsport.CurrencyReader // 币种快照换算（P2-09 T2；nil = 同币直收）
	settings  settingsport.Provider       // site/url 等（P2-09 T5 回调地址拼接；nil = 相对路径）
	supplier  port.SupplierRecharger      // target=supply 充值入账（供货账户余额；nil = 未装配跳过）
}

// NewPaymentRepoImpl 构造。
func NewPaymentRepoImpl(d *data.Data, box *crypto.Box, reg *Registry, lifecycle orderport.OrderLifecycle, wallet walletport.Wallet, points walletport.Points, outbox events.Writer, currency settingsport.CurrencyReader, settings settingsport.Provider, supplier port.SupplierRecharger) *PaymentRepoImpl {
	return &PaymentRepoImpl{data: d, Cipher: box, reg: reg, lifecycle: lifecycle, wallet: wallet, points: points, outbox: outbox, currency: currency, settings: settings, supplier: supplier}
}

// ChargeSnapshot 币种快照换算（P2-09 T2）：
// 渠道凭据 target_currency（空/CNY）→ 同币直收（units=amount, rate=1, currency=CNY）；
// 否则 currency 表 rate/precision → money.ToDisplay（decimal 精确、四舍五入）。
// rate 缺失/非法 → 1:1 直通（safeRate 语义——宁可同币直收不错换）。
type ChargeSnapshot struct {
	Units    int64
	Currency string
	Rate     float64
}

func (r *PaymentRepoImpl) computeCharge(ctx context.Context, cfg json.RawMessage, amount money.Cents) ChargeSnapshot {
	direct := ChargeSnapshot{Units: 0, Currency: "", Rate: 0} // 0 units = 同币直收路径
	var probe struct {
		TargetCurrency string `json:"target_currency"`
	}
	if json.Unmarshal(cfg, &probe) != nil || probe.TargetCurrency == "" {
		return direct
	}
	tc := strings.ToUpper(strings.TrimSpace(probe.TargetCurrency))
	if tc == "CNY" || r.currency == nil {
		return direct
	}
	rateStr, precision, err := r.currency.CurrencyByCode(ctx, tc)
	if err != nil || rateStr == "" {
		return direct // 币种未配置：同币直收（fail-safe，渠道侧以 CNY 收）
	}
	rate, err := decimal.NewFromString(rateStr)
	if err != nil || rate.IsZero() || rate.IsNegative() {
		return direct
	}
	ex := money.ToDisplay(amount, rate, precision)
	rf, _ := rate.Float64()
	return ChargeSnapshot{Units: ex.DisplayAmount, Currency: tc, Rate: rf}
}

// snapshotCharge 渠道发起成功后固化快照三列（跨币路径；同币直收零写）。
// 失败仅日志不阻断（快照缺失时回调走旧核对路径——fail-safe）。
func (r *PaymentRepoImpl) snapshotCharge(ctx context.Context, paymentID uint64, snap ChargeSnapshot) {
	if snap.Units == 0 {
		return
	}
	_, _ = data.Client(ctx, r.data).Payment.UpdateOneID(paymentID).
		SetChargedUnits(snap.Units).
		SetChargedCurrency(snap.Currency).
		SetExchangeRate(snap.Rate).
		Save(ctx)
}

// ── 渠道管理（T1）────────────────────────────────────────────

// ListChannels 渠道列表（凭据脱敏）。
func (r *PaymentRepoImpl) ListChannels(ctx context.Context) ([]*ent.PaymentChannel, error) {
	return data.Client(ctx, r.data).PaymentChannel.Query().
		Order(ent.Asc(paymentchannel.FieldSort)).
		All(ctx)
}

// ChannelMethod 支付方式（收银台顾客看到的选项；params 承载网关路由参数）。
type ChannelMethod struct {
	Code    string            `json:"code"`
	Name    string            `json:"name"`
	Icon    string            `json:"icon,omitempty"`
	Enabled bool              `json:"enabled"`
	Params  map[string]string `json:"params,omitempty"`
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
	for _, m := range ms {
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
func (r *PaymentRepoImpl) CreateChannel(ctx context.Context, name, code, driver, configJSON string, fee int64, feeType string, enabled bool, sort int32, icon string, methods []map[string]any) (*ent.PaymentChannel, error) {
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
	if methods != nil {
		q = q.SetMethods(methods)
	}
	return q.Save(ctx)
}

// UpdateChannel 更新渠道（config_json=**** 跳过凭据修改；feeType 空=不修改；
// setIcon/setMethods=false 保持原值——proto optional 语义）。
func (r *PaymentRepoImpl) UpdateChannel(ctx context.Context, id uint64, name, configJSON string, fee int64, feeType string, enabled bool, sort int32, setIcon bool, icon string, setMethods bool, methods []map[string]any) (*ent.PaymentChannel, error) {
	q := data.Client(ctx, r.data).PaymentChannel.UpdateOneID(id)
	if name != "" {
		q.SetName(name)
	}
	if configJSON != "" && configJSON != `"****"` {
		ch, err := data.Client(ctx, r.data).PaymentChannel.Get(ctx, id)
		if err != nil {
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
	return data.Client(ctx, r.data).PaymentChannel.DeleteOneID(id).Exec(ctx)
}

// DecryptConfig 解密凭据（失败降级为空，铁律 5）。
func (r *PaymentRepoImpl) DecryptConfig(ch *ent.PaymentChannel) json.RawMessage {
	plain, err := r.Cipher.Open(ch.Config, []byte("payment_channel:"+ch.Code))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(plain)
}

// CallbackURL 渠道回调地址（站点 URL 拼接；site/url 未配置回落相对路径——
// P2-09 T5：admin 配置面板展示复制，填到支付平台 webhook/notify 配置）。
func (r *PaymentRepoImpl) CallbackURL(ctx context.Context, code string) string {
	base := ""
	if r.settings != nil {
		if raw, err := r.settings.Get(ctx, "site", "url"); err == nil && len(raw) > 2 {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				base = strings.TrimRight(v, "/")
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

// ── 支付单（T4）────────────────────────────────────────────

// CreatePayment 创建支付单。
func (r *PaymentRepoImpl) CreatePayment(ctx context.Context, orderID uint64, channel string, amount int64, idemKey string) (*ent.Payment, error) {
	return data.Client(ctx, r.data).Payment.Create().
		SetOrderID(orderID).
		SetChannel(channel).
		SetAmount(amount).
		SetStatus(payment.StatusPending).
		SetIdempotencyKey(idemKey).
		Save(ctx)
}

// CreateRechargePayment 充值支付单（RechargePayer 端口实现）：
// 建单（关联 recharge_order_id）→ 渠道发起（金额=服务端落库值）→ 返回跳转信息。
func (r *PaymentRepoImpl) CreateRechargePayment(ctx context.Context, rechargeOrderID uint64, channel string, amount money.Cents) (*port.RechargePaymentInfo, error) {
	client := data.Client(ctx, r.data)
	ro, err := client.RechargeOrder.Get(ctx, rechargeOrderID)
	if err != nil {
		return nil, fmt.Errorf("payment.RECHARGE_NOT_FOUND")
	}
	ch, err := client.PaymentChannel.Query().
		Where(paymentchannel.Code(channel), paymentchannel.Enabled(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("payment.CHANNEL_NOT_FOUND")
	}
	if err != nil {
		return nil, err
	}
	if ch.Driver == "wallet" {
		return nil, fmt.Errorf("payment.RECHARGE_CHANNEL_INVALID: 充值不支持余额渠道")
	}
	p, err := client.Payment.Create().
		SetRechargeOrderID(ro.ID).
		SetChannel(channel).
		SetAmount(ro.Amount).
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
	snap := r.computeCharge(ctx, cfg, amount)
	info, err := provider.CreatePayment(ctx, port.CreatePaymentRequest{
		OrderNo:      fmt.Sprintf("RCH%d", ro.ID),
		Channel:      channel,
		Amount:       amount,
		Subject:      "余额充值",
		ChargedUnits: snap.Units, ChargedCurrency: snap.Currency,
		NotifyBaseURL: "/payments/callback/" + channel,
		Config:        cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("payment.CREATE_FAILED: %w", err)
	}
	r.snapshotCharge(ctx, p.ID, snap)
	return &port.RechargePaymentInfo{
		PaymentID: p.ID, Type: info.Type, Payload: string(info.Payload),
	}, nil
}

// GetPayment 按 ID 查支付单。
func (r *PaymentRepoImpl) GetPayment(ctx context.Context, id uint64) (*ent.Payment, error) {
	return data.Client(ctx, r.data).Payment.Get(ctx, id)
}

// ListPayments 支付单列表。
func (r *PaymentRepoImpl) ListPayments(ctx context.Context, status, orderNo string, cursor uint64, limit int32) ([]*ent.Payment, error) {
	q := data.Client(ctx, r.data).Payment.Query().
		Order(ent.Desc(payment.FieldID)).
		Limit(int(limit))
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

// HandleCallback 回调处理管线（P1-04 核心——四重校验+幂等+业务推进）。
// 事务内：行锁 payment → 幂等三层 → 按支付单类型分流：
//
//	订单型（order_id>0）：余额渠道先扣款（wallet.DebitInTx，同事务）→
//	  OrderLifecycle.MarkPaid（状态机 CAS + 状态事件 + outbox order.paid，§4.6 破环点）
//	充值型（recharge_order_id>0）：充值单 pending→success → 余额入账
//	  （amount+gift，reference=recharge:<paymentID> 幂等）→ outbox recharge.succeeded
//
// 支付确认前零入账（铁律 16）；事件与入账同事务，回滚不残留。
func (r *PaymentRepoImpl) HandleCallback(ctx context.Context, paymentID uint64, fact CallbackFact) error {
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

		// 2) 幂等第一层：已 success 直接 ACK
		if p.Status == payment.StatusSuccess {
			return nil
		}

		// 3) 四重校验（渠道/单号/金额/币种——金额永远对服务端权威值）
		//    跨币路径（P2-09 T2 快照）：回调金额=渠道币种最小单位，对 charged_units
		//    精确核对（网关回显下单金额，零二次换算）；实收换算回基础货币分落 charged_amount。
		//    同币路径（charged_units=0）：旧口径 fact.Amount==amount + CNY。
		if fact.Channel != p.Channel {
			return fmt.Errorf("payment.CHANNEL_MISMATCH")
		}
		chargedBase := fact.Amount
		if p.ChargedUnits > 0 {
			if fact.Amount != p.ChargedUnits {
				return fmt.Errorf("payment.AMOUNT_MISMATCH: want units %d got %d", p.ChargedUnits, fact.Amount)
			}
			if !strings.EqualFold(fact.Currency, p.ChargedCurrency) {
				return fmt.Errorf("payment.CURRENCY_MISMATCH: want %s got %s", p.ChargedCurrency, fact.Currency)
			}
			// 实收换算回基础货币分（快照汇率；decimal 精确）
			if rate := decimal.NewFromFloat(p.ExchangeRate); !rate.IsZero() {
				prec := int32(2)
				if r.currency != nil {
					if _, p32, err := r.currency.CurrencyByCode(ctx, p.ChargedCurrency); err == nil {
						prec = p32
					}
				}
				base, _ := money.FromDisplay(fact.Amount, rate, prec)
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

		// 4) 幂等第二层 + 分流推进（支付单 CAS pending→success）
		now := time.Now().UTC()
		_, err = client.Payment.Update().
			Where(payment.ID(p.ID), payment.StatusEQ(payment.StatusPending)).
			SetStatus(payment.StatusSuccess).
			SetChargedAmount(chargedBase).
			SetChannelOrderNo(fact.ChannelOrderNo).
			SetPaidAt(now).
			Save(txCtx)
		if err != nil {
			return err
		}

		if p.RechargeOrderID > 0 {
			return r.settleRecharge(txCtx, p, fact)
		}
		if fact.OrderNo == "" && p.OrderID > 0 {
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
	if r.wallet != nil && r.isWalletChannel(ctx, p.Channel) && p.OrderID > 0 {
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
	ro, err := client.RechargeOrder.Get(ctx, p.RechargeOrderID)
	if err != nil {
		return err
	}
	if ro.Status != rechargeorder.StatusPending {
		return fmt.Errorf("payment.RECHARGE_NOT_PENDING")
	}
	if _, err := client.RechargeOrder.UpdateOneID(ro.ID).
		SetStatus(rechargeorder.StatusSuccess).
		SetPaymentID(p.ID).
		SetPaidAt(time.Now().UTC()).
		Save(ctx); err != nil {
		return err
	}
	// 入账分支（target 由建单时定；金额全部取服务端落库值，铁律 16）：
	//   balance → 用户钱包余额（本金+赠送）+ 赠送积分；
	//   supply  → 对接账户供货余额（本金；供货预存无赠送）。
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
	// 赠送积分（幂等键 points:recharge:<paymentID>；积分账本 M1b）
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
	Channel        string
	ChannelOrderNo string
	OrderNo        string
	Amount         int64 // 分（基础货币）
	Currency       string
	Success        bool
	Raw            json.RawMessage
}

// ── 退款（T6）───────────────────────────────────────────────

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
		Id: ch.ID, Name: ch.Name, Code: ch.Code, Driver: ch.Driver,
		ConfigJson: `"****"`, // 凭据永不明文下发
		Fee:        ch.Fee, FeeType: string(ch.FeeType),
		Enabled: ch.Enabled, Sort: ch.Sort,
		Icon: ch.Icon,
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
		AmountCents: rf.Amount, Channel: string(rf.Channel),
		Status: string(rf.Status), Reason: rf.Reason,
		UpstreamRefundId: rf.UpstreamRefundID,
	}
}

// RefundOrder 订单退款入口（P2-02 procurement 失败策略消费，通道 A）：
// 按订单创建退款单（channel=upstream），订单 refund 流转由 payment 现有编排驱动。
func (r *PaymentRepoImpl) RefundOrder(ctx context.Context, orderID uint64, amount money.Cents, reason string) error {
	if amount <= 0 {
		o, err := data.Client(ctx, r.data).Order.Get(ctx, orderID)
		if err != nil {
			return fmt.Errorf("payment: 退款订单不存在: %w", err)
		}
		amount = money.Cents(o.TotalAmount)
	}
	if amount <= 0 {
		return fmt.Errorf("payment: 退款金额必须为正")
	}
	_, err := r.CreateRefund(ctx, orderID, int64(amount), string(refundorder.ChannelUpstream), reason)
	return err
}
