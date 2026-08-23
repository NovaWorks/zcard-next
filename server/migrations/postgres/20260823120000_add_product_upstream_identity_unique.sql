-- 货源同步幂等唯一约束（PG 大数量采集优化）：
-- 历史数据可能存在同 (subsite_id, upstream_source_id, upstream_product_code) 重复
-- （旧同步为先查后写无约束兜底），先保留最小 id 清理，再加唯一索引。
DELETE FROM "products" p USING "products" q
WHERE p."id" > q."id"
  AND p."subsite_id" = q."subsite_id"
  AND p."upstream_source_id" = q."upstream_source_id"
  AND p."upstream_product_code" = q."upstream_product_code"
  AND p."upstream_product_code" IS NOT NULL;
-- Create unique index "product_subsite_id_upstream_source_id_upstream_product_code" to table: "products"
CREATE UNIQUE INDEX "product_subsite_id_upstream_source_id_upstream_product_code" ON "products" ("subsite_id", "upstream_source_id", "upstream_product_code");
