-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_order_items" table
CREATE TABLE `new_order_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `sku_id` integer NULL, `product_name` text NOT NULL DEFAULT (''), `form_answers` json NULL, `assigned_admin_id` integer NOT NULL DEFAULT (0), `sku_name` text NULL, `unit_price` integer NOT NULL, `quantity` integer NOT NULL, `amount` integer NOT NULL, `cost` integer NOT NULL DEFAULT (0), `fulfillment_type` text NOT NULL, `delivery_source_id` integer NULL, `fulfillment_status` text NOT NULL DEFAULT ('pending'), `commission_snapshot` json NULL, `profit_snapshot` json NULL, `order_id` integer NOT NULL, CONSTRAINT `order_items_orders_items` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Copy rows from old table "order_items" to new temporary table "new_order_items"
INSERT INTO `new_order_items` (`id`, `created_at`, `updated_at`, `subsite_id`, `product_id`, `sku_id`, `product_name`, `form_answers`, `assigned_admin_id`, `sku_name`, `unit_price`, `quantity`, `amount`, `cost`, `fulfillment_type`, `fulfillment_status`, `commission_snapshot`, `profit_snapshot`, `order_id`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `product_id`, `sku_id`, `product_name`, `form_answers`, `assigned_admin_id`, `sku_name`, `unit_price`, `quantity`, `amount`, `cost`, `fulfillment_type`, `fulfillment_status`, `commission_snapshot`, `profit_snapshot`, `order_id` FROM `order_items`;
-- Drop "order_items" table after copying rows
DROP TABLE `order_items`;
-- Rename temporary table "new_order_items" to "order_items"
ALTER TABLE `new_order_items` RENAME TO `order_items`;
-- Create index "orderitem_delivery_source_id" to table: "order_items"
CREATE INDEX `orderitem_delivery_source_id` ON `order_items` (`delivery_source_id`);
-- Create index "orderitem_order_id" to table: "order_items"
CREATE INDEX `orderitem_order_id` ON `order_items` (`order_id`);
-- Create index "orderitem_product_id" to table: "order_items"
CREATE INDEX `orderitem_product_id` ON `order_items` (`product_id`);
-- Add column "category_protected" to table: "products"
ALTER TABLE `products` ADD COLUMN `category_protected` bool NOT NULL DEFAULT false;
-- Create "product_delivery_sources" table
CREATE TABLE `product_delivery_sources` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `purchase_key` text NULL, `current_key` text NULL, `status` text NOT NULL DEFAULT ('empty'), `content` blob NULL, `connection_id` integer NOT NULL DEFAULT (0), `upstream_product` text NOT NULL DEFAULT (''), `upstream_sku` text NOT NULL DEFAULT (''), `upstream_order_id` text NOT NULL DEFAULT (''), `connection_revision` text NOT NULL DEFAULT (''), `origin_procurement_id` integer NOT NULL DEFAULT (0), `expires_at` integer NOT NULL DEFAULT (0), `delivered_count` integer NOT NULL DEFAULT (0), `max_deliveries` integer NOT NULL DEFAULT (0), `submitted_at` integer NOT NULL DEFAULT (0), `next_check_at` integer NOT NULL DEFAULT (0), `exchange_rate` real NOT NULL DEFAULT (1), `cost_cents` integer NOT NULL DEFAULT (0), `last_error` text NOT NULL DEFAULT (''));
-- Create index "product_delivery_sources_purchase_key_key" to table: "product_delivery_sources"
CREATE UNIQUE INDEX `product_delivery_sources_purchase_key_key` ON `product_delivery_sources` (`purchase_key`);
-- Create index "productdeliverysource_current_key" to table: "product_delivery_sources"
CREATE UNIQUE INDEX `productdeliverysource_current_key` ON `product_delivery_sources` (`current_key`);
-- Create index "productdeliverysource_subsite_id_product_id_sku_id" to table: "product_delivery_sources"
CREATE INDEX `productdeliverysource_subsite_id_product_id_sku_id` ON `product_delivery_sources` (`subsite_id`, `product_id`, `sku_id`);
-- Create index "productdeliverysource_status_next_check_at" to table: "product_delivery_sources"
CREATE INDEX `productdeliverysource_status_next_check_at` ON `product_delivery_sources` (`status`, `next_check_at`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
