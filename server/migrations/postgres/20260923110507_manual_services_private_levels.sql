-- Modify "member_levels" table
ALTER TABLE "member_levels" ADD COLUMN "acquire_mode" character varying NOT NULL DEFAULT 'auto', ADD COLUMN "display_mode" character varying NOT NULL DEFAULT 'public';
-- Modify "order_deliveries" table
ALTER TABLE "order_deliveries" ADD COLUMN "service_content" bytea NULL, ADD COLUMN "delivered_quantity" integer NOT NULL DEFAULT 0;
-- Modify "order_items" table
ALTER TABLE "order_items" ADD COLUMN "product_name" character varying NOT NULL DEFAULT '', ADD COLUMN "form_answers" jsonb NULL, ADD COLUMN "assigned_admin_id" bigint NOT NULL DEFAULT 0;
-- Modify "product_controls" table
ALTER TABLE "product_controls" ADD COLUMN "placeholder" character varying NOT NULL DEFAULT '', ADD COLUMN "validation" character varying NOT NULL DEFAULT 'text', ADD COLUMN "max_length" integer NOT NULL DEFAULT 500;
-- Modify "product_skus" table
ALTER TABLE "product_skus" ADD COLUMN "fulfillment_mode" character varying NOT NULL DEFAULT 'follow';
-- Modify "products" table
ALTER TABLE "products" ADD COLUMN "fulfillment_mode" character varying NOT NULL DEFAULT 'auto', ADD COLUMN "manual_stock" bigint NOT NULL DEFAULT -1;
-- Modify "supplier_product_prices" table
ALTER TABLE "supplier_product_prices" ALTER COLUMN "scope" TYPE character varying;
-- Modify "users" table
ALTER TABLE "users" ADD COLUMN "manual_level_id" bigint NOT NULL DEFAULT 0;
