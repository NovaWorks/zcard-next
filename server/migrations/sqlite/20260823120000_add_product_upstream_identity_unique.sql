-- 货源同步幂等唯一约束（PG 大数量采集优化）：
-- 历史数据可能存在同 (subsite_id, upstream_source_id, upstream_product_code) 重复
-- （旧同步为先查后写无约束兜底），先保留最小 id 清理，再加唯一索引。
DELETE FROM "products" WHERE "upstream_product_code" IS NOT NULL AND "id" NOT IN (
  SELECT MIN("id") FROM "products" WHERE "upstream_product_code" IS NOT NULL
  GROUP BY "subsite_id", "upstream_source_id", "upstream_product_code"
);
CREATE UNIQUE INDEX "product_subsite_id_upstream_source_id_upstream_product_code" ON "products" ("subsite_id", "upstream_source_id", "upstream_product_code");
