-- Add column "commerce_version" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `commerce_version` integer NOT NULL DEFAULT 0;
-- Add column "shipping_amount" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `shipping_amount` integer NOT NULL DEFAULT 0;
-- Add column "shipping_status" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `shipping_status` text NOT NULL DEFAULT 'none';
-- Add column "shipping_address" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `shipping_address` json NULL;
-- Add column "request_hash" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `request_hash` text NOT NULL DEFAULT '';
-- Add column "item_allocations" to table: "refund_orders"
ALTER TABLE `refund_orders` ADD COLUMN `item_allocations` json NULL;
-- Add column "shipping_amount" to table: "refund_orders"
ALTER TABLE `refund_orders` ADD COLUMN `shipping_amount` integer NOT NULL DEFAULT 0;
-- Add column "physical_stock" to table: "product_skus"
ALTER TABLE `product_skus` ADD COLUMN `physical_stock` integer NOT NULL DEFAULT 0;
-- Add column "goods_type" to table: "products"
ALTER TABLE `products` ADD COLUMN `goods_type` text NOT NULL DEFAULT 'virtual';
-- Add column "shipping_mode" to table: "products"
ALTER TABLE `products` ADD COLUMN `shipping_mode` text NOT NULL DEFAULT 'free';
-- Add column "shipping_fee" to table: "products"
ALTER TABLE `products` ADD COLUMN `shipping_fee` integer NOT NULL DEFAULT 0;
-- Add column "shipping_countries" to table: "products"
ALTER TABLE `products` ADD COLUMN `shipping_countries` json NULL;
-- Add column "physical_stock" to table: "products"
ALTER TABLE `products` ADD COLUMN `physical_stock` integer NOT NULL DEFAULT 0;
-- Add column "goods_type" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `goods_type` text NOT NULL DEFAULT 'virtual';
-- Add column "paid_amount" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `paid_amount` integer NOT NULL DEFAULT 0;
-- Add column "shipping_amount" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `shipping_amount` integer NOT NULL DEFAULT 0;
-- Add column "refunded_amount" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `refunded_amount` integer NOT NULL DEFAULT 0;
-- Add column "refunded_shipping" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `refunded_shipping` integer NOT NULL DEFAULT 0;
-- Add column "canceled_quantity" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `canceled_quantity` integer NOT NULL DEFAULT 0;
-- Add column "shipped_quantity" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `shipped_quantity` integer NOT NULL DEFAULT 0;
-- Add column "received_quantity" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `received_quantity` integer NOT NULL DEFAULT 0;
-- Add column "returned_quantity" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `returned_quantity` integer NOT NULL DEFAULT 0;
-- Create "physical_stock_movements" table
CREATE TABLE `physical_stock_movements` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `order_id` integer NOT NULL DEFAULT (0), `delta` integer NOT NULL, `reference` text NOT NULL, `reason` text NOT NULL);
-- Create index "physical_stock_movements_reference_key" to table: "physical_stock_movements"
CREATE UNIQUE INDEX `physical_stock_movements_reference_key` ON `physical_stock_movements` (`reference`);
-- Create index "physicalstockmovement_product_id_sku_id" to table: "physical_stock_movements"
CREATE INDEX `physicalstockmovement_product_id_sku_id` ON `physical_stock_movements` (`product_id`, `sku_id`);
-- Create index "physicalstockmovement_order_id" to table: "physical_stock_movements"
CREATE INDEX `physicalstockmovement_order_id` ON `physical_stock_movements` (`order_id`);
-- Create "shipments" table
CREATE TABLE `shipments` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `order_id` integer NOT NULL, `carrier` text NOT NULL, `tracking_no` text NOT NULL, `items` json NOT NULL, `address` json NOT NULL, `status` text NOT NULL DEFAULT ('shipped'), `admin_id` integer NOT NULL, `request_key` text NOT NULL, `received_at` integer NOT NULL DEFAULT (0));
-- Create index "shipments_request_key_key" to table: "shipments"
CREATE UNIQUE INDEX `shipments_request_key_key` ON `shipments` (`request_key`);
-- Create index "shipment_order_id" to table: "shipments"
CREATE INDEX `shipment_order_id` ON `shipments` (`order_id`);
-- Create index "shipment_subsite_id_status" to table: "shipments"
CREATE INDEX `shipment_subsite_id_status` ON `shipments` (`subsite_id`, `status`);
