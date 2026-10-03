-- Modify "email_verifications" table
ALTER TABLE `email_verifications` MODIFY COLUMN `purpose` enum('register','phone_register','reset','order_access') NOT NULL;
-- Modify "order_items" table
ALTER TABLE `order_items` ADD COLUMN `inventory_tracked` bool NOT NULL DEFAULT 1;
-- Modify "orders" table
ALTER TABLE `orders` ADD COLUMN `order_access_token_hash` varchar(64) NOT NULL DEFAULT "", ADD COLUMN `order_access_token_secret` blob NULL;
-- Modify "products" table
ALTER TABLE `products` ADD COLUMN `product_property` varchar(16) NOT NULL DEFAULT "", ADD COLUMN `track_inventory` bool NOT NULL DEFAULT 1, ADD COLUMN `sales_visible` bool NOT NULL DEFAULT 1;
-- Create "physical_return_receipts" table
CREATE TABLE `physical_return_receipts` (`id` bigint unsigned NOT NULL AUTO_INCREMENT, `created_at` datetime(3) NOT NULL, `updated_at` datetime(3) NOT NULL, `subsite_id` bigint unsigned NOT NULL DEFAULT 0, `order_id` bigint unsigned NOT NULL, `item_id` bigint unsigned NOT NULL, `quantity` int NOT NULL, `request_key` varchar(128) NOT NULL, `request_hash` varchar(64) NOT NULL, `reason` varchar(120) NOT NULL, `inventory_tracked` bool NOT NULL, PRIMARY KEY (`id`), INDEX `physicalreturnreceipt_item_id` (`item_id`), INDEX `physicalreturnreceipt_order_id` (`order_id`), UNIQUE INDEX `request_key` (`request_key`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;

-- Preserve the physical/virtual attribute of existing catalog rows.
UPDATE products SET product_property = goods_type WHERE product_property = '';
