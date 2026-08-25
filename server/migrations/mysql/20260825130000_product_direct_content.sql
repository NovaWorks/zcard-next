-- 链接/兑换码直发：商品级直发内容密文列（url/code 类型商品同一内容反复发货）。
-- SQLite/PG 该列为 blob/bytea 无需约束；仅加列。
ALTER TABLE `products` ADD COLUMN `direct_content` blob NULL;
-- 交付模式扩枚举：direct = 商品级直发内容（无卡密引用）。
ALTER TABLE `order_deliveries` MODIFY `delivered_mode` enum('status','delete','direct') NOT NULL;
