-- 商品名放宽 150 → 1024：部分上游渠道把完整描述塞进商品名
-- （tghao Facebook 类最长 517 字节），同步撞 ent 校验与 MySQL varchar 硬限。
ALTER TABLE `products` MODIFY `name` varchar(1024) NOT NULL;
