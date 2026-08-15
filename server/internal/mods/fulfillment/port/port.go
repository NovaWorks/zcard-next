// Package port 为 fulfillment 模块对外契约（零依赖包）。
package port

import "context"

// Fulfiller 履约窄接口（订阅 order.paid / payment.succeeded 后执行，通道 C 异步）。
type Fulfiller interface {
	// FulfillOrder 履约订单（本地卡密：reserved → used 标记/即删两模式；
	// 交付记录 = 卡密引用 + 一次性令牌，无明文快照，§5.20.2）。
	FulfillOrder(ctx context.Context, orderNo string) error
}
