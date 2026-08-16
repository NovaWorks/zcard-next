-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_procurement_items" table
CREATE TABLE `new_procurement_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `procurement_id` integer NOT NULL, `upstream_sku` text NOT NULL DEFAULT (''), `quantity` integer NOT NULL, `unit_cost` integer NOT NULL DEFAULT (0), `received_content` json NULL);
-- Copy rows from old table "procurement_items" to new temporary table "new_procurement_items"
INSERT INTO `new_procurement_items` (`id`, `created_at`, `updated_at`, `procurement_id`, `upstream_sku`, `quantity`, `unit_cost`, `received_content`) SELECT `id`, `created_at`, `updated_at`, `procurement_id`, `upstream_sku`, `quantity`, `unit_cost`, `received_content` FROM `procurement_items`;
-- Drop "procurement_items" table after copying rows
DROP TABLE `procurement_items`;
-- Rename temporary table "new_procurement_items" to "procurement_items"
ALTER TABLE `new_procurement_items` RENAME TO `procurement_items`;
-- Create index "procurementitem_procurement_id" to table: "procurement_items"
CREATE INDEX `procurementitem_procurement_id` ON `procurement_items` (`procurement_id`);
-- Add column "last_error" to table: "procurement_orders"
ALTER TABLE `procurement_orders` ADD COLUMN `last_error` text NULL;
-- Add column "upstream_refund_id" to table: "procurement_orders"
ALTER TABLE `procurement_orders` ADD COLUMN `upstream_refund_id` text NULL;
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
