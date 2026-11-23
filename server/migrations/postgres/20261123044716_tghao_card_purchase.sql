-- Modify "order_items" table
ALTER TABLE "order_items" ADD COLUMN "upstream_card_cipher" bytea NULL;
-- Modify "product_controls" table
ALTER TABLE "product_controls" ADD COLUMN "option_labels" jsonb NULL;
