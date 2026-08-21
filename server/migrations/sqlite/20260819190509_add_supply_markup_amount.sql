-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_supply_connections" table
CREATE TABLE `new_supply_connections` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `driver` text NOT NULL, `base_url` text NOT NULL, `credentials` blob NOT NULL, `status` text NOT NULL DEFAULT ('active'), `callback_url` text NULL, `retry_max` integer NOT NULL DEFAULT (5), `retry_intervals` text NOT NULL DEFAULT ('[30,60,300]'), `exchange_rate` real NOT NULL DEFAULT (1), `price_markup_percent` real NOT NULL DEFAULT (0), `price_markup_amount` integer NOT NULL DEFAULT (0), `price_rounding_mode` text NOT NULL DEFAULT ('none'), `auto_sync_price` bool NOT NULL DEFAULT (true), `stock_mode` text NOT NULL DEFAULT ('real'), `settings` json NULL, `last_ping_at` datetime NULL, `last_ping_ok` bool NOT NULL DEFAULT (false), `last_synced_at` datetime NULL, `last_error` text NULL, `balance_cache` integer NOT NULL DEFAULT (0), `last_collect_at` datetime NULL, `last_price_sync_at` datetime NULL, `last_status_sync_at` datetime NULL, `rate_state` json NULL, `rate_limit_until` datetime NULL);
-- Copy rows from old table "supply_connections" to new temporary table "new_supply_connections"
INSERT INTO `new_supply_connections` (`id`, `created_at`, `updated_at`, `name`, `driver`, `base_url`, `credentials`, `status`, `callback_url`, `retry_max`, `retry_intervals`, `exchange_rate`, `price_markup_percent`, `price_rounding_mode`, `auto_sync_price`, `stock_mode`, `settings`, `last_ping_at`, `last_ping_ok`, `last_synced_at`, `last_error`, `balance_cache`, `last_collect_at`, `last_price_sync_at`, `last_status_sync_at`, `rate_state`, `rate_limit_until`) SELECT `id`, `created_at`, `updated_at`, `name`, `driver`, `base_url`, `credentials`, `status`, `callback_url`, `retry_max`, `retry_intervals`, `exchange_rate`, `price_markup_percent`, `price_rounding_mode`, `auto_sync_price`, `stock_mode`, `settings`, `last_ping_at`, `last_ping_ok`, `last_synced_at`, `last_error`, `balance_cache`, `last_collect_at`, `last_price_sync_at`, `last_status_sync_at`, `rate_state`, `rate_limit_until` FROM `supply_connections`;
-- Drop "supply_connections" table after copying rows
DROP TABLE `supply_connections`;
-- Rename temporary table "new_supply_connections" to "supply_connections"
ALTER TABLE `new_supply_connections` RENAME TO `supply_connections`;
-- Create index "supplyconnection_status_driver" to table: "supply_connections"
CREATE INDEX `supplyconnection_status_driver` ON `supply_connections` (`status`, `driver`);
-- Create index "supplyconnection_last_synced_at" to table: "supply_connections"
CREATE INDEX `supplyconnection_last_synced_at` ON `supply_connections` (`last_synced_at`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
