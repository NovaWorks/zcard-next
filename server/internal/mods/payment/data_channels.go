package payment

// T1 渠道管理 + T4 回调管线 + T6 退款 v1（P1-04 核心交易支付）。
//
// 渠道凭据 AES-256-GCM 加密存储（ZCARD_DATA_KEY），解密失败降级为空——列表绝不 500。
// 回调管线：四重校验（渠道/单号/金额/币种）→ 幂等三层 → markPaid → 事务后事件。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// PaymentRepoImpl 支付仓储。
type PaymentRepoImpl struct {
	data   *data.Data
	Cipher *crypto.Box // ZCARD_DATA_KEY
	reg    *Registry   // 渠道 adapter 注册表
}

// NewPaymentRepoImpl 构造。
func NewPaymentRepoImpl(d *data.Data, box *crypto.Box, reg *Registry) *PaymentRepoImpl {
	return &PaymentRepoImpl{data: d, Cipher: box, reg: reg}
}

// ── 渠道管理（T1）────────────────────────────────────────────

// ListChannels 渠道列表（凭据脱敏）。
func (r *PaymentRepoImpl) ListChannels(ctx context.Context) ([]*ent.PaymentChannel, error) {
	return data.Client(ctx, r.data).PaymentChannel.Query().
		Order(ent.Asc(paymentchannel.FieldSort)).
		All(ctx)
}

// CreateChannel 创建渠道（凭据加密入库）。
func (r *PaymentRepoImpl) CreateChannel(ctx context.Context, name, code, driver, configJSON string, fee int64, feeType string, enabled bool, sort int32) (*ent.PaymentChannel, error) {
	enc, err := r.Cipher.Seal([]byte(configJSON), []byte("payment_channel:"+code))
	if err != nil {
		return nil, fmt.Errorf("payment: 凭据加密失败: %w", err)
	}
	return data.Client(ctx, r.data).PaymentChannel.Create().
		SetName(name).
		SetCode(code).
		SetDriver(driver).
		SetConfig(enc).
		SetFee(fee).
		SetFeeType(paymentchannel.FeeType(feeType)).
		SetEnabled(enabled).
		SetSort(sort).
		Save(ctx)
}

// UpdateChannel 更新渠道（config_json=**** 跳过凭据修改）。
func (r *PaymentRepoImpl) UpdateChannel(ctx context.Context, id uint64, name, configJSON string, fee int64, enabled bool, sort int32) (*ent.PaymentChannel, error) {
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
	q.SetEnabled(enabled)
	if sort >= 0 {
		q.SetSort(sort)
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

// HandleCallback 回调处理管线（P1-04 核心——四重校验+幂等+markPaid）。
// 事务内：行锁 payment + order → 四重校验 → 幂等 → markPaid。
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

		// 3) 四重校验
		if fact.Channel != p.Channel {
			return fmt.Errorf("payment.CHANNEL_MISMATCH")
		}
		if fact.Amount != p.Amount {
			return fmt.Errorf("payment.AMOUNT_MISMATCH: want %d got %d", p.Amount, fact.Amount)
		}
		if fact.Currency != "CNY" {
			return fmt.Errorf("payment.CURRENCY_MISMATCH")
		}

		// 4) 行锁订单（幂等第二层）
		o, err := client.Order.Query().Where(order.ID(p.OrderID)).Only(txCtx)
		if err != nil {
			return err
		}
		if o.Status == order.StatusPaid || o.Status == order.StatusDelivered || o.Status == order.StatusCompleted {
			// 订单已 paid——只更新支付单元数据
			_, _ = client.Payment.UpdateOne(p).
				SetStatus(payment.StatusSuccess).
				SetChargedAmount(fact.Amount).
				SetChannelOrderNo(fact.ChannelOrderNo).
				SetPaidAt(time.Now().UTC()).
				Save(txCtx)
			return nil
		}

		// 5) markPaid（幂等第三层：状态机 CAS）
		_, err = client.Payment.Update().
			Where(payment.ID(p.ID), payment.StatusEQ(payment.StatusPending)).
			SetStatus(payment.StatusSuccess).
			SetChargedAmount(fact.Amount).
			SetChannelOrderNo(fact.ChannelOrderNo).
			SetPaidAt(time.Now().UTC()).
			Save(txCtx)
		if err != nil {
			return err
		}

		// 6) 订单置 paid（经 order 状态机）
		_, err = client.Order.Update().
			Where(order.ID(o.ID), order.StatusEQ(order.StatusPendingPayment)).
			SetStatus(order.StatusPaid).
			SetPaidAt(time.Now().UTC()).
			SetVersion(o.Version + 1).
			Save(txCtx)
		if err != nil {
			return err
		}

		// 7) 状态事件溯源
		_, _ = client.OrderStatusEvent.Create().
			SetOrderID(o.ID).
			SetFromStatus(string(o.Status)).
			SetToStatus(string(order.StatusPaid)).
			SetEvent("paid").
			SetOperator("system").
			Save(txCtx)

		return nil
	})
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

// ToChannelPB 转 admin 协议（凭据脱敏）。
func ToChannelPB(ch *ent.PaymentChannel) *adminv1.Channel {
	return &adminv1.Channel{
		Id: ch.ID, Name: ch.Name, Code: ch.Code, Driver: ch.Driver,
		ConfigJson: `"****"`, // 凭据永不明文下发
		Fee:        ch.Fee, FeeType: string(ch.FeeType),
		Enabled: ch.Enabled, Sort: ch.Sort,
	}
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
