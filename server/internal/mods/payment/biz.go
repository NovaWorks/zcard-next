// Package payment 支付模块（M1a）：渠道/支付单/回调管线/补单/退款编排。
//
// M0 落地注册表骨架；M1a 交付 alipay/wechat/epay/钱包四渠道 + 回调管线
// （事务内：行锁 payment+order → 四重校验 → 幂等 → markPaid）+ golden vector 契约测试
// （固定 key + 固定 body → 期望签名/验签结果，1.x 最大测试缺口的门禁化）。
package payment

import (
	"fmt"
	"sync"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

// ErrProviderUnknown 渠道未注册（新增渠道 = 新增 adapter 文件 + 注册）。
type ErrProviderUnknown struct{ Provider string }

func (e *ErrProviderUnknown) Error() string {
	return fmt.Sprintf("payment: 渠道 %q 未注册", e.Provider)
}

// registry 渠道注册表（启动装配：各 adapter 自注册）。
type registry struct {
	mu        sync.RWMutex
	providers map[string]port.Provider
}

// Register 注册渠道 adapter（init/装配期调用）。
func (r *registry) Register(p port.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Type()] = p
}

// Provider 按渠道取 adapter。
func (r *registry) Provider(provider string) (port.Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[provider]
	if !ok {
		return nil, &ErrProviderUnknown{Provider: provider}
	}
	return p, nil
}

// NewRegistry 构造空注册表。
func NewRegistry() *registry { return &registry{providers: map[string]port.Provider{}} }

var _ port.Registry = (*registry)(nil)

// PaymentUsecase 支付用例骨架（M1a 交付 CreatePayment/HandleCallback/退款编排）。
type PaymentUsecase struct {
	registry *registry
}

// NewPaymentUsecase 构造。
func NewPaymentUsecase(reg *registry) *PaymentUsecase {
	return &PaymentUsecase{registry: reg}
}
