// Package port 为 dashboard 模块对外契约（零依赖包）。
package port

import (
	"context"
	"errors"
	"time"
)

// ErrUpstreamListUnsupported 上游不支持订单列表（对账 job 置 failed 的可查原因）。
var ErrUpstreamListUnsupported = errors.New("上游协议不支持订单列表对账")

// UpstreamOrder 上游订单（对账比对侧数据；金额分）。
type UpstreamOrder struct {
	UpstreamOrderID string
	Amount          int64
	Status          string
}

// UpstreamOrderSource 上游订单数据源（对账引擎消费，通道 A）。
// supply.Gateway 实现（按连接构建 adapter 并转换为本类型）；测试注入假实现覆盖四态。
type UpstreamOrderSource interface {
	// ListOrders 时间窗内上游订单（不支持返回 ErrUpstreamListUnsupported）。
	ListOrders(ctx context.Context, connectionID uint64, start, end time.Time) ([]UpstreamOrder, error)
}
