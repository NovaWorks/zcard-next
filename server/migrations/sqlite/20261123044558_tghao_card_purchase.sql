-- Add column "option_labels" to table: "product_controls"
ALTER TABLE `product_controls` ADD COLUMN `option_labels` json NULL;
-- Add column "upstream_card_cipher" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `upstream_card_cipher` blob NULL;
