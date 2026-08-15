// Package tenancy 租户上下文（规划 §4.9 / §4.11）。
//
// 开源版只启用 Row 行级隔离（subsite_id，0=主站）；Schema/Database 模式与
// 远程库属商业版（字段已定、代码不开源，ADR-D15）。subsite_id 列在三种模式下
// 统一保留，Ent schema 定义不因模式而变。
//
// 本包只承载「业务形态无关」的租户上下文与纯领域逻辑；涉及 *ent.Client 的
// TenantStore 抽象（Resolve/Migrate/Export/Import/Ping）定义在 internal/data
// （ent 收口边界：platform 不 import ent，架构测试 §4.10-5）。
package tenancy

import (
	"context"
	"errors"
)

// 主站租户 ID。
const MainSubsiteID uint64 = 0

// Context 租户上下文（请求级，经中间件注入）。
// M1 交付 Ent interceptor：读拦截器自动追加 subsite_id 过滤、写拦截器从父实体
// 继承填充——业务代码不手写租户条件（铁律 14）。
type Context struct {
	// SubsiteID 租户 ID（0=主站）
	SubsiteID uint64
	// IsMain 是否主站请求
	IsMain bool
	// Host 请求 Host（域名解析中间件留痕，分站域名验证 M3）
	Host string
}

type ctxKey struct{}

// ErrNoTenant 上下文缺失租户信息（中间件未装配或非 HTTP 入口）。
var ErrNoTenant = errors.New("tenancy: 上下文缺失租户信息")

// WithContext 注入租户上下文。
func WithContext(ctx context.Context, tc Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, tc)
}

// FromContext 取出租户上下文；缺失时返回主站兜底（fail-open 到主站仅限
// 非 HTTP 入口如 worker：worker 消费事件时由事件载荷携带租户）。
func FromContext(ctx context.Context) Context {
	if tc, ok := ctx.Value(ctxKey{}).(Context); ok {
		return tc
	}
	return Context{SubsiteID: MainSubsiteID, IsMain: true}
}

// Main 主站上下文（worker/定时任务的兜底）。
func Main() Context { return Context{SubsiteID: MainSubsiteID, IsMain: true} }
