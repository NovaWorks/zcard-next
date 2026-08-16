// Package supplier 对外供货模块（P2-03 完整落地，本站作上游）：
//   - Supply API v2 全端点：ping/categories/products/stock/orders 查创退（HMAC 四头）
//   - HMAC 鉴权中间件：±300s 时间窗 + nonce 防重放（DB UNIQUE）+ 双口径验签（常数时间）
//   - 下单链路：downstream_order_no 幂等 → 供货价（覆盖价>基础价）→ 账本扣款
//     （幂等键 supply_order:<downID>，余额不足零流水）→ inventory.Reserve 锁卡
//     （与前台同一库存池防超卖）→ MarkUsed 交付 → 卡密内存态返回 → 回调登记
//   - 供货账本：append-only 流水 + balance_cache（对账由流水重算）
//   - 回调转发：HTTPS 强制 + SSRF 防护（httpx）+ 四头签名 + 退避重试（死信可手动重发）
//   - 下游管理：申请→审核→发 key（secret 只显一次）→启停/重置/定价/充值/流水
//
// 表：supplier_accounts / supplier_ledger_entries / supply_orders / supply_nonces /
// supplier_product_prices / downstream_callbacks。
// 协议对偶：P2-01 zcard 适配器即本协议的客户端（eat your own dog food）。
package supplier
