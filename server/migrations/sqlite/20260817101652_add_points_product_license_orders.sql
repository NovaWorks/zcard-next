-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_products" table
CREATE TABLE `new_products` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `category_id` integer NULL, `name` text NOT NULL, `slug` text NOT NULL, `description` text NULL, `cover` text NULL, `images` json NULL, `price` integer NOT NULL DEFAULT (0), `factory_price` integer NOT NULL DEFAULT (0), `draft_premium` integer NOT NULL DEFAULT (0), `member_price` json NULL, `points_required` integer NOT NULL DEFAULT (0), `stock_type` text NOT NULL DEFAULT ('card'), `stock_visible` bool NOT NULL DEFAULT (true), `delivery_mode` text NOT NULL DEFAULT ('status'), `control_config` json NULL, `dedup` bool NOT NULL DEFAULT (true), `sort` integer NOT NULL DEFAULT (0), `status` integer NOT NULL DEFAULT (1), `upstream_source_id` integer NULL, `upstream_product_code` text NULL, `upstream_synced_at` datetime NULL);
-- Copy rows from old table "products" to new temporary table "new_products"
INSERT INTO `new_products` (`id`, `created_at`, `updated_at`, `subsite_id`, `category_id`, `name`, `slug`, `description`, `cover`, `images`, `price`, `factory_price`, `draft_premium`, `member_price`, `stock_type`, `stock_visible`, `delivery_mode`, `control_config`, `dedup`, `sort`, `status`, `upstream_source_id`, `upstream_product_code`, `upstream_synced_at`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `category_id`, `name`, `slug`, `description`, `cover`, `images`, `price`, `factory_price`, `draft_premium`, `member_price`, `stock_type`, `stock_visible`, `delivery_mode`, `control_config`, `dedup`, `sort`, `status`, `upstream_source_id`, `upstream_product_code`, `upstream_synced_at` FROM `products`;
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
-- Create "license_orders" table
CREATE TABLE `license_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `plan` text NOT NULL, `amount` integer NOT NULL, `status` text NOT NULL DEFAULT ('pending'), `payer_user_id` integer NOT NULL, `instance_id` text NOT NULL, `domain` text NULL, `features` json NULL, `expires_at` datetime NOT NULL, `license_file` text NULL, `paid_at` datetime NULL);
-- Create index "licenseorder_payer_user_id" to table: "license_orders"
CREATE INDEX `licenseorder_payer_user_id` ON `license_orders` (`payer_user_id`);
-- Create index "licenseorder_instance_id" to table: "license_orders"
CREATE INDEX `licenseorder_instance_id` ON `license_orders` (`instance_id`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
