// Package coupon 营销模块（M1 券 / M3 秒杀）。
//
// 表：coupons / flash_sales / promotions。使用校验在价格管线中位置固定
// （会员折扣之后，§5.2）；订单取消/退款返还券（可配）；秒杀与卡密锁定同一把锁防超卖。
package coupon
