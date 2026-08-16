// Package port 为 fulfillment 模块对外契约（零依赖包）。
package port

import "context"

// Fulfiller 履约窄接口（订阅 order.paid / payment.succeeded 后执行，通道 C 异步）。
type Fulfiller interface {
	// FulfillOrder 履约订单（本地卡密：reserved → used 标记/即删两模式；
	// 交付记录 = 卡密引用 + 一次性令牌，无明文快照，§5.20.2）。
	FulfillOrder(ctx context.Context, orderNo string) error
}

// UpstreamDeliveryItem 上游交付条目（P2-02 T4 交付出口入参）。
type UpstreamDeliveryItem struct {
	// SealedContent 已加密卡密（procurement_items.received_content 原样透传，
	// 交付层不触碰明文——铁律 11 的出口侧约束）。
	SealedContent []byte
	// ContentHash 卡密 keyed hash（content_hash 列）。
	ContentHash string
}

// AttachUpstreamDelivery 上游采购交付出口（procurement 模块消费，通道 A）：
// 把已加密的上游卡密写为 cards（used）+ order_deliveries（一次性令牌/掩码/三重门），
// 与本地卡密同一交付出口（P2-02 验收：上游卡密库内零明文）。
type AttachUpstreamDelivery interface {
	AttachUpstreamDelivery(ctx context.Context, orderID, itemID, productID uint64, items []UpstreamDeliveryItem) error
}
