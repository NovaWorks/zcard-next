// Package supply 上游货源模块（ 完整落地）：
// - 协议适配器（adapter/）：zcard（自家 Supply v2，4 头 HMAC）/ dujiao_next（3 头）/
// acg_faka（body MD5），golden vector 契约测试，出站 100% 经 platform/httpx（架构规则 5）
// - 连接管理：CRUD + 凭据 AES-GCM 加密（AAD 绑定 driver+base_url）+ SSRF 校验
// - 同步服务：拉分类/商品 → upsert 本地（catalog port）→ up_stock 缓存 → 定价重算
// （汇率×加价×取整三模式；价格保护：auto_sync_price 关/固定覆盖价/运营改价不覆盖）
// - 任务追踪：心跳 30s / 统计计数 / 取消标志；终态发布 sync.completed
// - 健康度：Ping 探活（cron 5min）+ ping_history 累计（M4 供应商评分基础数据）
// - 下架语义：上游 inactive → 本地隐藏(2)；deleted 哨兵 → 本地下架(0)
//
// 表：supply_connections / supply_mappings / supply_sync_tasks。
// 1.x 协议知识迁移要点（CLAUDE.md）：acg-faka 路由按 / 拆段（路径参数走 body）、
// cards 按 PHP_EOL 拆分、tradeNo 口径、request_no 非幂等窗口；dujiao 分页 50/页 +
// includes_inactive 回声字段防旧版误判；zcard 签名「哈希字节 === 发出字节」。
package supply
