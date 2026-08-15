// Package wallet 钱包与账务模块（M1 充值 / M3 提现）。
//
// 账务纪律（§5.6，超越友商）：
//   - available/locked 冻结分离（total 恒等式）；
//   - 一切余额变动必须经 InTx：行锁账户 → 非负校验 → 乐观锁更新 → 写流水；
//   - reference 幂等键全账务统一（重复入账直接返回成功）；
//   - 余额永可由流水重算（balance_before/after 快照）。
package wallet

import (
	"errors"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// ErrInsufficientBalance 余额不足（订单不创建，明确错误码，§6.4）。
var ErrInsufficientBalance = errors.New("wallet.INSUFFICIENT_BALANCE")

// Reference 幂等键构造（全账务统一规范，数据库架构 §7.1）。
func Reference(kind string, ids ...uint64) string {
	s := kind
	for _, id := range ids {
		s = fmt.Sprintf("%s:%d", s, id)
	}
	return s
}

// 常用幂等键（附录 §5.6 / §7.1）。
const (
	RefOrderPay    = "order_pay"    // order_pay:<orderID>
	RefOrderRefund = "order_refund" // order_refund:<id>:<seq>
	RefRecharge    = "recharge"     // recharge:<payID>
	RefCommission  = "commission"   // commission:<id>
	RefAdjust      = "adjust"       // adjust:<auditID>（手动调账需审计）
)

// WalletUsecase 钱包用例骨架（M1 交付 InTx 完整实现与并发幂等测试）。
type WalletUsecase struct{}

// NewWalletUsecase 构造。
func NewWalletUsecase() *WalletUsecase { return &WalletUsecase{} }

// Total 账户总额（total = available + locked 恒真不变量）。
func Total(available, locked money.Cents) money.Cents { return available.Add(locked) }
