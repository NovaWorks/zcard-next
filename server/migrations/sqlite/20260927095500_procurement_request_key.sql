-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_procurement_orders" table
CREATE TABLE `new_procurement_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `order_item_id` integer NOT NULL, `connection_id` integer NOT NULL, `upstream_order_id` text NULL, `status` text NOT NULL DEFAULT ('pending'), `fail_strategy` text NOT NULL DEFAULT ('auto_refund'), `retry_count` integer NOT NULL DEFAULT (0), `next_retry_at` datetime NULL, `last_poll_at` datetime NULL, `dedupe_key` text NOT NULL, `trace_id` text NULL, `last_error` text NULL, `upstream_refund_id` text NULL);
-- Copy rows from old table "procurement_orders" to new temporary table "new_procurement_orders"
INSERT INTO `new_procurement_orders` (`id`, `created_at`, `updated_at`, `order_item_id`, `connection_id`, `upstream_order_id`, `status`, `fail_strategy`, `retry_count`, `next_retry_at`, `last_poll_at`, `dedupe_key`, `trace_id`, `last_error`, `upstream_refund_id`) SELECT `id`, `created_at`, `updated_at`, `order_item_id`, `connection_id`, `upstream_order_id`, `status`, `fail_strategy`, `retry_count`, `next_retry_at`, `last_poll_at`, `dedupe_key`, `trace_id`, `last_error`, `upstream_refund_id` FROM `procurement_orders`;
-- Drop "procurement_orders" table after copying rows
DROP TABLE `procurement_orders`;
-- Rename temporary table "new_procurement_orders" to "procurement_orders"
ALTER TABLE `new_procurement_orders` RENAME TO `procurement_orders`;
-- Create index "procurement_orders_dedupe_key_key" to table: "procurement_orders"
CREATE UNIQUE INDEX `procurement_orders_dedupe_key_key` ON `procurement_orders` (`dedupe_key`);
-- Create index "procurementorder_connection_id_upstream_order_id" to table: "procurement_orders"
CREATE INDEX `procurementorder_connection_id_upstream_order_id` ON `procurement_orders` (`connection_id`, `upstream_order_id`);
-- Create index "procurementorder_order_item_id" to table: "procurement_orders"
CREATE UNIQUE INDEX `procurementorder_order_item_id` ON `procurement_orders` (`order_item_id`);
-- Create index "procurementorder_status_last_poll_at" to table: "procurement_orders"
CREATE INDEX `procurementorder_status_last_poll_at` ON `procurement_orders` (`status`, `last_poll_at`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
