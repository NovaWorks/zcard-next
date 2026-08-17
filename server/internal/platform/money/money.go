// Package money 定义全仓唯一金额类型（铁律 1）：
// 金额一律 int64「分」，基于基础货币（默认 CNY）；存储与传递永不出现浮点。
// 跨币种换算仅发生在展示层与下单快照，中间过程用 decimal 库（shopspring）后取整。
package money

import (
	"errors"
	"fmt"
	"strings"
)

// Cents 金额（分，基础货币）。禁止定义第二个金额类型（架构测试 §4.10-6）。
type Cents int64

// Zero 零元。
const Zero Cents = 0

// ErrInvalidAmount 金额非法（解析失败/精度越界/负数越界场景）。
var ErrInvalidAmount = errors.New("money: invalid amount")

// MaxCents 单笔金额上限（1 亿元 = 10^10 分）。管理面提交金额的服务端边界
// 校验口径（铁律 16）：客户端可提交的金额必须落在 [0, MaxCents]（有符号口径
// 为 ±MaxCents），超限即拒绝——抓包改金额只能在允许区间内取值。
const MaxCents int64 = 10_000_000_00

// ValidCents 非负且不超上限（价格/充值等非负金额口径）。
func ValidCents(v int64) bool { return v >= 0 && v <= MaxCents }

// ValidSignedCents 有符号金额（调账/账本口径；绝对值不超上限）。
func ValidSignedCents(v int64) bool { return v >= -MaxCents && v <= MaxCents }

// Add 返回 a+b（不改变原值；金额是不可变语义）。
func (c Cents) Add(other Cents) Cents { return c + other }

// Sub 返回 a-b。
func (c Cents) Sub(other Cents) Cents { return c - other }

// Mul 返回 a*n（数量乘单价；n 为非负数量）。
func (c Cents) Mul(n int32) Cents { return c * Cents(n) }

// Neg 返回 -a（行式金额模型中折扣为负值的构造入口）。
func (c Cents) Neg() Cents { return -c }

// IsNegative 是否为负。
func (c Cents) IsNegative() bool { return c < 0 }

// IsZero 是否为零。
func (c Cents) IsZero() bool { return c == 0 }

// String 按「分」输出（不做币种格式化；展示层用 Format）。
func (c Cents) String() string { return fmt.Sprintf("%d", int64(c)) }

// Format 按指定小数位输出十进制字符串（纯整数运算，无浮点参与）。
// precision 为货币小数位（CNY=2、JPY=0）；四舍五入模式由调用方在换算入口决定。
func (c Cents) Format(precision int) string {
	if precision < 0 {
		precision = 0
	}
	neg := c < 0
	if neg {
		c = -c
	}
	base := int64(1)
	for i := 0; i < precision; i++ {
		base *= 10
	}
	whole := int64(c) / base
	frac := int64(c) % base
	s := fmt.Sprintf("%d", whole)
	if precision > 0 {
		s += "." + fmt.Sprintf("%0*d", precision, frac)
	}
	if neg {
		s = "-" + s
	}
	return s
}

// ParseDecimalStr 从十进制字符串解析为分（如 "12.34" → 1234）。
// 禁止 float 中转；precision 为字符串允许的小数位上限（越界报错，不静默截断）。
func ParseDecimalStr(s string, precision int) (Cents, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%w: empty string", ErrInvalidAmount)
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	if hasFrac && (fracPart == "" || len(fracPart) > precision) {
		return 0, fmt.Errorf("%w: fraction %q exceeds precision %d", ErrInvalidAmount, fracPart, precision)
	}
	var cents int64
	for _, ch := range []byte(intPart) {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
		}
		cents = cents*10 + int64(ch-'0')
	}
	for i := 0; i < precision; i++ {
		cents *= 10
	}
	if hasFrac {
		for i, ch := range []byte(fracPart) {
			digit := int64(ch - '0')
			// 逐位放到对应精度位（如 precision=2："34" → 3*10+4）
			cents += digit * pow10(int64(precision-i-1))
		}
	}
	if neg {
		cents = -cents
	}
	return Cents(cents), nil
}

func pow10(n int64) int64 {
	v := int64(1)
	for i := int64(0); i < n; i++ {
		v *= 10
	}
	return v
}
