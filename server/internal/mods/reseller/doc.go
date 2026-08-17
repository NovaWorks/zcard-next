// Package reseller 分站模块（M3）：申请/审核、域名 DNS 验证、白标配置、
// 分站定价（SKU>商品>分站默认）、分账账本、提现、分站等级权限位、
// 分站自营商品上架（出单段旅程首环）+ order.paid 分账订阅（reseller.settle）。
//
// 表：reseller_profiles / reseller_sites / reseller_pricing / reseller_ledger_entries /
// reseller_balance_accounts / reseller_related_accounts。
// 开源版隔离 = subsite_id 行级（Row 模式）；品牌隔离 fail-closed（绝不暴露主站品牌）。
package reseller
