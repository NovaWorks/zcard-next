-- Add column "order_access_token_hash" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `order_access_token_hash` text NOT NULL DEFAULT '';
-- Add column "order_access_token_secret" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `order_access_token_secret` blob NULL;
-- Add column "product_property" to table: "products"
ALTER TABLE `products` ADD COLUMN `product_property` text NOT NULL DEFAULT '';
-- Add column "track_inventory" to table: "products"
ALTER TABLE `products` ADD COLUMN `track_inventory` bool NOT NULL DEFAULT true;
-- Add column "sales_visible" to table: "products"
ALTER TABLE `products` ADD COLUMN `sales_visible` bool NOT NULL DEFAULT true;
-- Add column "inventory_tracked" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `inventory_tracked` bool NOT NULL DEFAULT true;
-- Create "physical_return_receipts" table
CREATE TABLE `physical_return_receipts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `order_id` integer NOT NULL, `item_id` integer NOT NULL, `quantity` integer NOT NULL, `request_key` text NOT NULL, `request_hash` text NOT NULL, `reason` text NOT NULL, `inventory_tracked` bool NOT NULL);
-- Create index "physical_return_receipts_request_key_key" to table: "physical_return_receipts"
CREATE UNIQUE INDEX `physical_return_receipts_request_key_key` ON `physical_return_receipts` (`request_key`);
-- Create index "physicalreturnreceipt_order_id" to table: "physical_return_receipts"
CREATE INDEX `physicalreturnreceipt_order_id` ON `physical_return_receipts` (`order_id`);
-- Create index "physicalreturnreceipt_item_id" to table: "physical_return_receipts"
CREATE INDEX `physicalreturnreceipt_item_id` ON `physical_return_receipts` (`item_id`);

-- Preserve the physical/virtual attribute of existing catalog rows.
UPDATE products SET product_property = goods_type WHERE product_property = '';
