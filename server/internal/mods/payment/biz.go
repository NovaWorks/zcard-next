// Package payment 支付模块（M1a）：渠道/支付单/回调管线/补单/退款编排。
//
// 落地注册表骨架；M1a 交付 alipay/wechat/epay/钱包四渠道 + 回调管线
// （事务内：行锁 payment+order → 四重校验 → 幂等 → markPaid）+ golden vector 契约测试
// （固定 key + 固定 body → 期望签名/验签结果，1.x 最大测试缺口的门禁化）。
package payment

import (
	"fmt"
	"sort"
	"sync"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

// ErrProviderUnknown 渠道未注册（新增渠道 = 新增 adapter 文件 + 注册）。
type ErrProviderUnknown struct{ Provider string }

func (e *ErrProviderUnknown) Error() string {
	return fmt.Sprintf("payment: 渠道 %q 未注册", e.Provider)
}

// Registry 渠道注册表（启动装配：各 adapter 自注册）。
type Registry struct {
	mu        sync.RWMutex
	providers map[string]port.Provider
}

// Register 注册渠道 adapter（init/装配期调用）。
func (r *Registry) Register(p port.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Type()] = p
}

// Provider 按渠道取 adapter。
func (r *Registry) Provider(provider string) (port.Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[provider]
	if !ok {
		return nil, &ErrProviderUnknown{Provider: provider}
	}
	return p, nil
}

// All 全部已注册 adapter（：admin 配置面驱动元数据遍历）。
func (r *Registry) All() []port.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]port.Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type() < out[j].Type() })
	return out
}

// NewRegistry 构造注册表并注册内置渠道 adapter（alipay/wechat/epay）。
func NewRegistry() *Registry {
	r := &Registry{providers: map[string]port.Provider{}}
	// 内置渠道注册失败仅可能因实现缺陷（如重复 Type），这里直接 panic 暴露问题。
	if err := adapter.RegisterAll(r); err != nil {
		panic(err)
	}
	return r
}

var _ port.Registry = (*Registry)(nil)

// PaymentUsecase 支付用例骨架（M1a 交付 CreatePayment/HandleCallback/退款编排）。
type PaymentUsecase struct {
	registry *Registry
}

// NewPaymentUsecase 构造。
func NewPaymentUsecase(reg *Registry) *PaymentUsecase {
	return &PaymentUsecase{registry: reg}
}
