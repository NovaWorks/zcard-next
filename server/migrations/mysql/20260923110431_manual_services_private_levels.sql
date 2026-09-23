-- Modify "member_levels" table
ALTER TABLE `member_levels` ADD COLUMN `acquire_mode` varchar(255) NOT NULL DEFAULT "auto", ADD COLUMN `display_mode` varchar(255) NOT NULL DEFAULT "public";
-- Modify "order_deliveries" table
ALTER TABLE `order_deliveries` ADD COLUMN `service_content` blob NULL, ADD COLUMN `delivered_quantity` int NOT NULL DEFAULT 0;
-- Modify "order_items" table
ALTER TABLE `order_items` ADD COLUMN `product_name` varchar(255) NOT NULL DEFAULT "", ADD COLUMN `form_answers` json NULL, ADD COLUMN `assigned_admin_id` bigint unsigned NOT NULL DEFAULT 0;
-- Modify "product_controls" table
ALTER TABLE `product_controls` ADD COLUMN `placeholder` varchar(255) NOT NULL DEFAULT "", ADD COLUMN `validation` varchar(255) NOT NULL DEFAULT "text", ADD COLUMN `max_length` int NOT NULL DEFAULT 500;
-- Modify "product_skus" table
ALTER TABLE `product_skus` ADD COLUMN `fulfillment_mode` varchar(255) NOT NULL DEFAULT "follow";
-- Modify "products" table
ALTER TABLE `products` ADD COLUMN `fulfillment_mode` varchar(255) NOT NULL DEFAULT "auto", ADD COLUMN `manual_stock` bigint NOT NULL DEFAULT -1;
-- Modify "users" table
ALTER TABLE `users` ADD COLUMN `manual_level_id` bigint unsigned NOT NULL DEFAULT 0;
