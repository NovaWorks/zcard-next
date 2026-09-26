-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_supply_mappings" table
CREATE TABLE `new_supply_mappings` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `connection_id` integer NOT NULL, `upstream_category` text NULL, `local_category_id` integer NULL, `upstream_product` text NOT NULL, `local_product_id` integer NULL, `upstream_sku` text NOT NULL DEFAULT (''), `local_sku_id` integer NULL, `stock_probe_after` integer NOT NULL DEFAULT 0, `stock_probe_lease` integer NOT NULL DEFAULT 0, `stock_probe_failures` integer NOT NULL DEFAULT 0, `up_stock` integer NOT NULL DEFAULT (0), `stock_checked_at` datetime NULL, `stock_reference` integer NOT NULL DEFAULT (-2), `stock_reference_at` datetime NULL, `pricing_override` json NULL);
-- Copy rows from old table "supply_mappings" to new temporary table "new_supply_mappings"
INSERT INTO `new_supply_mappings` (`id`, `created_at`, `updated_at`, `connection_id`, `upstream_category`, `local_category_id`, `upstream_product`, `local_product_id`, `upstream_sku`, `local_sku_id`, `up_stock`, `stock_checked_at`, `stock_reference`, `stock_reference_at`, `pricing_override`) SELECT `id`, `created_at`, `updated_at`, `connection_id`, `upstream_category`, `local_category_id`, `upstream_product`, `local_product_id`, `upstream_sku`, `local_sku_id`, `up_stock`, `stock_checked_at`, `stock_reference`, `stock_reference_at`, `pricing_override` FROM `supply_mappings`;
-- Drop "supply_mappings" table after copying rows
DROP TABLE `supply_mappings`;
-- Rename temporary table "new_supply_mappings" to "supply_mappings"
ALTER TABLE `new_supply_mappings` RENAME TO `supply_mappings`;
-- Create index "supplymapping_connection_id_upstream_product_upstream_sku" to table: "supply_mappings"
CREATE UNIQUE INDEX `supplymapping_connection_id_upstream_product_upstream_sku` ON `supply_mappings` (`connection_id`, `upstream_product`, `upstream_sku`);
-- Create index "supplymapping_local_product_id" to table: "supply_mappings"
CREATE INDEX `supplymapping_local_product_id` ON `supply_mappings` (`local_product_id`);
-- Create index "supplymapping_connection_id_stock_probe_after_id" to table: "supply_mappings"
CREATE INDEX `supplymapping_connection_id_stock_probe_after_id` ON `supply_mappings` (`connection_id`, `stock_probe_after`, `id`);
-- Add column "low_stock_scanned_at" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `low_stock_scanned_at` integer NOT NULL DEFAULT 0;
-- Add column "low_stock_message" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `low_stock_message` text NOT NULL DEFAULT '';
-- Create "stock_alerts" table
CREATE TABLE `stock_alerts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `source_key` text NOT NULL, `threshold` integer NOT NULL DEFAULT (0), `state` integer NOT NULL DEFAULT (0), `notified_state` integer NOT NULL DEFAULT (0), `last_notified_at` integer NOT NULL DEFAULT (0));
-- Create index "stockalert_product_id_sku_id" to table: "stock_alerts"
CREATE UNIQUE INDEX `stockalert_product_id_sku_id` ON `stock_alerts` (`product_id`, `sku_id`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
