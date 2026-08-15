// Package fulfillment 本地卡密履约模块（M1）：标记/即删两模式、交付留言、邮件通知。
//
// 2.0 关键改造（1.x 痛点反转）：交付不写明文 OrderDelivery；只存「卡密引用 +
// 一次性取货令牌」，取货时现场解密返回、不落库明文（铁律 11/§5.20.2）。
package fulfillment

// FulfillmentUsecase 履约用例骨架（M1 交付 FulfillOrder 编排：
// 锁定卡密 → 置 used/即删 → 写交付记录（引用+令牌）→ outbox(order.delivered) →
// 按商品开关发交付邮件[notify]）。
type FulfillmentUsecase struct{}

// NewFulfillmentUsecase 构造。
func NewFulfillmentUsecase() *FulfillmentUsecase { return &FulfillmentUsecase{} }
