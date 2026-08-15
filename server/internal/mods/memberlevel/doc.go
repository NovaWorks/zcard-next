// Package memberlevel 会员等级模块（M1 等级 / M3 升级自动化）。
//
// 表：member_levels。升级条件（充值/消费/双条件）、等级折扣、积分产生规则；
// 升级由支付成功事件异步评估（只升不降，可配）。1.x UserGroup/MemberUpgradeService 对齐。
package memberlevel
