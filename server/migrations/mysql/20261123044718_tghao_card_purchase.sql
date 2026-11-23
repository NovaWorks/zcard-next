-- Modify "order_items" table
ALTER TABLE `order_items` ADD COLUMN `upstream_card_cipher` blob NULL;
-- Modify "product_controls" table
ALTER TABLE `product_controls` MODIFY COLUMN `type` enum('text','password','select','number','checkbox','radio','textarea') NOT NULL, ADD COLUMN `option_labels` json NULL;
