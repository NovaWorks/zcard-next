-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_order_deliveries" table
CREATE TABLE `new_order_deliveries` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `item_id` integer NOT NULL, `card_id` integer NOT NULL, `delivery_token_hash` text NOT NULL, `service_content` blob NULL, `delivered_quantity` integer NOT NULL DEFAULT (0), `delivered_mode` text NOT NULL, `delivered_by` integer NOT NULL DEFAULT (0), `logistics` json NULL, `fetch_count` integer NOT NULL DEFAULT (0), `delivered_at` datetime NULL, `fetched_ip` text NULL, `order_id` integer NOT NULL, CONSTRAINT `order_deliveries_orders_deliveries` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Copy rows from old table "order_deliveries" to new temporary table "new_order_deliveries"
INSERT INTO `new_order_deliveries` (`id`, `created_at`, `updated_at`, `item_id`, `card_id`, `delivery_token_hash`, `delivered_mode`, `delivered_by`, `logistics`, `fetch_count`, `delivered_at`, `fetched_ip`, `order_id`) SELECT `id`, `created_at`, `updated_at`, `item_id`, `card_id`, `delivery_token_hash`, `delivered_mode`, `delivered_by`, `logistics`, `fetch_count`, `delivered_at`, `fetched_ip`, `order_id` FROM `order_deliveries`;
-- Drop "order_deliveries" table after copying rows
DROP TABLE `order_deliveries`;
-- Rename temporary table "new_order_deliveries" to "order_deliveries"
ALTER TABLE `new_order_deliveries` RENAME TO `order_deliveries`;
-- Create index "orderdelivery_order_id" to table: "order_deliveries"
CREATE INDEX `orderdelivery_order_id` ON `order_deliveries` (`order_id`);
-- Create index "orderdelivery_card_id" to table: "order_deliveries"
CREATE INDEX `orderdelivery_card_id` ON `order_deliveries` (`card_id`);
-- Create "new_order_items" table
CREATE TABLE `new_order_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `sku_id` integer NULL, `product_name` text NOT NULL DEFAULT (''), `form_answers` json NULL, `assigned_admin_id` integer NOT NULL DEFAULT (0), `sku_name` text NULL, `unit_price` integer NOT NULL, `quantity` integer NOT NULL, `amount` integer NOT NULL, `cost` integer NOT NULL DEFAULT (0), `fulfillment_type` text NOT NULL, `fulfillment_status` text NOT NULL DEFAULT ('pending'), `commission_snapshot` json NULL, `profit_snapshot` json NULL, `order_id` integer NOT NULL, CONSTRAINT `order_items_orders_items` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Copy rows from old table "order_items" to new temporary table "new_order_items"
INSERT INTO `new_order_items` (`id`, `created_at`, `updated_at`, `subsite_id`, `product_id`, `sku_id`, `sku_name`, `unit_price`, `quantity`, `amount`, `cost`, `fulfillment_type`, `fulfillment_status`, `commission_snapshot`, `profit_snapshot`, `order_id`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `product_id`, `sku_id`, `sku_name`, `unit_price`, `quantity`, `amount`, `cost`, `fulfillment_type`, `fulfillment_status`, `commission_snapshot`, `profit_snapshot`, `order_id` FROM `order_items`;
-- Drop "order_items" table after copying rows
DROP TABLE `order_items`;
-- Rename temporary table "new_order_items" to "order_items"
ALTER TABLE `new_order_items` RENAME TO `order_items`;
-- Create index "orderitem_order_id" to table: "order_items"
CREATE INDEX `orderitem_order_id` ON `order_items` (`order_id`);
-- Create index "orderitem_product_id" to table: "order_items"
CREATE INDEX `orderitem_product_id` ON `order_items` (`product_id`);
-- Create "new_product_skus" table
CREATE TABLE `new_product_skus` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `spec_values` json NOT NULL, `price` integer NULL, `cost` integer NULL, `fulfillment_mode` text NOT NULL DEFAULT ('follow'), `stock_offset` integer NOT NULL DEFAULT (0), `upstream_sku_id` text NULL, `product_id` integer NOT NULL, CONSTRAINT `product_skus_products_skus` FOREIGN KEY (`product_id`) REFERENCES `products` (`id`) ON DELETE NO ACTION);
-- Copy rows from old table "product_skus" to new temporary table "new_product_skus"
INSERT INTO `new_product_skus` (`id`, `created_at`, `updated_at`, `subsite_id`, `name`, `spec_values`, `price`, `cost`, `stock_offset`, `upstream_sku_id`, `product_id`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `name`, `spec_values`, `price`, `cost`, `stock_offset`, `upstream_sku_id`, `product_id` FROM `product_skus`;
-- Drop "product_skus" table after copying rows
DROP TABLE `product_skus`;
-- Rename temporary table "new_product_skus" to "product_skus"
ALTER TABLE `new_product_skus` RENAME TO `product_skus`;
-- Create index "productsku_product_id" to table: "product_skus"
CREATE INDEX `productsku_product_id` ON `product_skus` (`product_id`);
-- Create index "productsku_product_id_name" to table: "product_skus"
CREATE UNIQUE INDEX `productsku_product_id_name` ON `product_skus` (`product_id`, `name`);
-- Create "new_member_levels" table
CREATE TABLE `new_member_levels` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `logo` text NULL, `badge_color` text NULL, `threshold_type` text NOT NULL DEFAULT ('recharge'), `threshold_recharge` integer NOT NULL DEFAULT (0), `threshold_consume` integer NOT NULL DEFAULT (0), `acquire_mode` text NOT NULL DEFAULT ('auto'), `display_mode` text NOT NULL DEFAULT ('public'), `discount` integer NOT NULL DEFAULT (0), `points_rule` json NULL, `sort` integer NOT NULL DEFAULT (0), `enabled` bool NOT NULL DEFAULT (true));
-- Copy rows from old table "member_levels" to new temporary table "new_member_levels"
INSERT INTO `new_member_levels` (`id`, `created_at`, `updated_at`, `name`, `logo`, `badge_color`, `threshold_type`, `threshold_recharge`, `threshold_consume`, `discount`, `points_rule`, `sort`, `enabled`) SELECT `id`, `created_at`, `updated_at`, `name`, `logo`, `badge_color`, `threshold_type`, `threshold_recharge`, `threshold_consume`, `discount`, `points_rule`, `sort`, `enabled` FROM `member_levels`;
-- Drop "member_levels" table after copying rows
DROP TABLE `member_levels`;
-- Rename temporary table "new_member_levels" to "member_levels"
ALTER TABLE `new_member_levels` RENAME TO `member_levels`;
-- Create "new_product_controls" table
CREATE TABLE `new_product_controls` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `name` text NOT NULL, `type` text NOT NULL, `placeholder` text NOT NULL DEFAULT (''), `validation` text NOT NULL DEFAULT ('text'), `max_length` integer NOT NULL DEFAULT (500), `required` bool NOT NULL DEFAULT (false), `options` json NULL, `sort` integer NOT NULL DEFAULT (0));
-- Copy rows from old table "product_controls" to new temporary table "new_product_controls"
INSERT INTO `new_product_controls` (`id`, `created_at`, `updated_at`, `subsite_id`, `product_id`, `name`, `type`, `required`, `options`, `sort`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `product_id`, `name`, `type`, `required`, `options`, `sort` FROM `product_controls`;
-- Drop "product_controls" table after copying rows
DROP TABLE `product_controls`;
-- Rename temporary table "new_product_controls" to "product_controls"
ALTER TABLE `new_product_controls` RENAME TO `product_controls`;
-- Create index "productcontrol_product_id" to table: "product_controls"
CREATE INDEX `productcontrol_product_id` ON `product_controls` (`product_id`);
-- Create "new_users" table
CREATE TABLE `new_users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `manual_level_id` integer NOT NULL DEFAULT (0), `username` text NOT NULL, `email` text NULL, `phone` text NULL, `password_hash` text NULL, `status` text NOT NULL DEFAULT ('active'), `last_login_at` datetime NULL, `invite_l1` integer NULL, `invite_l2` integer NULL, `invite_l3` integer NULL, `promo_code` text NULL);
-- Copy rows from old table "users" to new temporary table "new_users"
INSERT INTO `new_users` (`id`, `created_at`, `updated_at`, `username`, `email`, `phone`, `password_hash`, `status`, `last_login_at`, `invite_l1`, `invite_l2`, `invite_l3`, `promo_code`) SELECT `id`, `created_at`, `updated_at`, `username`, `email`, `phone`, `password_hash`, `status`, `last_login_at`, `invite_l1`, `invite_l2`, `invite_l3`, `promo_code` FROM `users`;
-- Drop "users" table after copying rows
DROP TABLE `users`;
-- Rename temporary table "new_users" to "users"
ALTER TABLE `new_users` RENAME TO `users`;
-- Create index "users_username_key" to table: "users"
CREATE UNIQUE INDEX `users_username_key` ON `users` (`username`);
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX `users_email_key` ON `users` (`email`);
-- Create index "users_phone_key" to table: "users"
CREATE UNIQUE INDEX `users_phone_key` ON `users` (`phone`);
-- Create index "users_promo_code_key" to table: "users"
CREATE UNIQUE INDEX `users_promo_code_key` ON `users` (`promo_code`);
-- Create index "user_invite_l1" to table: "users"
CREATE INDEX `user_invite_l1` ON `users` (`invite_l1`);
-- Create "new_products" table
CREATE TABLE `new_products` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `category_id` integer NULL, `name` text NOT NULL, `slug` text NOT NULL, `description` text NULL, `cover` text NULL, `images` json NULL, `cover_protected` bool NOT NULL DEFAULT (false), `description_protected` bool NOT NULL DEFAULT (false), `price` integer NOT NULL DEFAULT (0), `factory_price` integer NOT NULL DEFAULT (0), `draft_premium` integer NOT NULL DEFAULT (0), `member_price` json NULL, `points_required` integer NOT NULL DEFAULT (0), `stock_type` text NOT NULL DEFAULT ('card'), `direct_content` blob NULL, `stock_visible` bool NOT NULL DEFAULT (true), `fulfillment_mode` text NOT NULL DEFAULT ('auto'), `manual_stock` integer NOT NULL DEFAULT (-1), `delivery_mode` text NOT NULL DEFAULT ('status'), `control_config` json NULL, `dedup` bool NOT NULL DEFAULT (true), `sort` integer NOT NULL DEFAULT (0), `is_recommend` bool NOT NULL DEFAULT (false), `status` integer NOT NULL DEFAULT (1), `upstream_source_id` integer NULL, `upstream_product_code` text NULL, `upstream_synced_at` datetime NULL);
-- Copy rows from old table "products" to new temporary table "new_products"
INSERT INTO `new_products` (`id`, `created_at`, `updated_at`, `subsite_id`, `category_id`, `name`, `slug`, `description`, `cover`, `images`, `cover_protected`, `description_protected`, `price`, `factory_price`, `draft_premium`, `member_price`, `points_required`, `stock_type`, `direct_content`, `stock_visible`, `delivery_mode`, `control_config`, `dedup`, `sort`, `is_recommend`, `status`, `upstream_source_id`, `upstream_product_code`, `upstream_synced_at`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `category_id`, `name`, `slug`, `description`, `cover`, `images`, IFNULL(`cover_protected`, (false)) AS `cover_protected`, IFNULL(`description_protected`, (false)) AS `description_protected`, `price`, `factory_price`, `draft_premium`, `member_price`, `points_required`, `stock_type`, `direct_content`, `stock_visible`, `delivery_mode`, `control_config`, `dedup`, `sort`, `is_recommend`, `status`, `upstream_source_id`, `upstream_product_code`, `upstream_synced_at` FROM `products`;
-- Drop "products" table after copying rows
DROP TABLE `products`;
-- Rename temporary table "new_products" to "products"
ALTER TABLE `new_products` RENAME TO `products`;
-- Create index "product_subsite_id_slug" to table: "products"
CREATE UNIQUE INDEX `product_subsite_id_slug` ON `products` (`subsite_id`, `slug`);
-- Create index "product_subsite_id_category_id" to table: "products"
CREATE INDEX `product_subsite_id_category_id` ON `products` (`subsite_id`, `category_id`);
-- Create index "product_subsite_id_status" to table: "products"
CREATE INDEX `product_subsite_id_status` ON `products` (`subsite_id`, `status`);
-- Create index "product_upstream_source_id" to table: "products"
CREATE INDEX `product_upstream_source_id` ON `products` (`upstream_source_id`);
-- Create index "product_subsite_id_upstream_source_id_upstream_product_code" to table: "products"
CREATE UNIQUE INDEX `product_subsite_id_upstream_source_id_upstream_product_code` ON `products` (`subsite_id`, `upstream_source_id`, `upstream_product_code`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
