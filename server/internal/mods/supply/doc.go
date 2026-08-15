// Package supply 上游货源模块（M2）：上游连接、协议适配器（zcard/dujiao_next/acg_faka）、
// 商品映射、库存价格同步（low 队列 + fail-open 兜底）、同步任务追踪。
//
// 表：supply_connections / supply_mappings / supply_sync_tasks。
// 安全（§5.7.3）：出站 SSRF 校验（platform/httpx）、DNS rebinding 防护、重定向逐跳重校验、
// 凭据 AES-GCM 加密、CreateOrder 幂等键、上游卡密到手即用 ZCARD_CARD_KEY 重加密。
// 三个协议适配器迁移 1.x 协议知识而非代码，配 golden vector 契约测试。
package supply
