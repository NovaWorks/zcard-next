// Package affiliate 三级分销模块（M3）：归因（注册/下单绑定上级，三级链快照进订单）、
// 佣金计算/冻结/确认（pending_confirm → available → withdrawn）、返利、退款逆向扣回。
//
// 表：affiliate_commissions（UNIQUE(order_id, tier) 防重发佣）；账务纪律与 wallet 一致。
package affiliate
