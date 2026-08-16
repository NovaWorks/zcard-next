-- Modify "procurement_items" table
-- 旧列为 bytea（M0 全量基线）；密文行 JSON 化——bytea 无自动 cast，经 text 中转。
-- 开发期列内无数据（功能未上线），DROP+ADD 等价且最简。
ALTER TABLE "procurement_items" DROP COLUMN "received_content";
ALTER TABLE "procurement_items" ADD COLUMN "received_content" jsonb NULL;
-- Modify "procurement_orders" table
ALTER TABLE "procurement_orders" ADD COLUMN "last_error" text NULL, ADD COLUMN "upstream_refund_id" character varying NULL;
