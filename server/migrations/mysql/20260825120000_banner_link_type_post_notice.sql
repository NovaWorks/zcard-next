-- 横幅跳转类型扩展：post=文章详情 /posts/:slug、notice=打开公告弹窗。
-- PG/SQLite 该列为文本无枚举约束，仅 MySQL 需要扩枚举。
ALTER TABLE `banners` MODIFY `link_type` enum('product','category','url','ad','post','notice') NOT NULL DEFAULT "url";
