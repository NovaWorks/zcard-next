-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_supply_sync_tasks" table
CREATE TABLE `new_supply_sync_tasks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `connection_id` integer NOT NULL, `mode` text NOT NULL, `scope` text NULL, `force_reprice` bool NOT NULL DEFAULT (false), `request_key` text NULL, `request_hash` text NULL, `import_payload` json NULL, `status` text NOT NULL DEFAULT ('pending'), `total_count` integer NOT NULL DEFAULT (0), `processed_count` integer NOT NULL DEFAULT (0), `created_count` integer NOT NULL DEFAULT (0), `updated_count` integer NOT NULL DEFAULT (0), `price_updated_count` integer NOT NULL DEFAULT (0), `manual_skipped_count` integer NOT NULL DEFAULT (0), `hidden_count` integer NOT NULL DEFAULT (0), `deleted_count` integer NOT NULL DEFAULT (0), `error_code` text NULL, `error_context` text NULL, `started_at` datetime NULL, `heartbeat_at` datetime NULL, `current_stage` text NULL, `current_page` integer NOT NULL DEFAULT (0), `cancel_requested_at` datetime NULL, `worker_version` text NULL, `finished_at` datetime NULL);
-- Copy rows from old table "supply_sync_tasks" to new temporary table "new_supply_sync_tasks"
INSERT INTO `new_supply_sync_tasks` (`id`, `created_at`, `updated_at`, `connection_id`, `mode`, `scope`, `force_reprice`, `status`, `total_count`, `processed_count`, `created_count`, `updated_count`, `price_updated_count`, `manual_skipped_count`, `hidden_count`, `deleted_count`, `error_code`, `error_context`, `started_at`, `heartbeat_at`, `current_stage`, `current_page`, `cancel_requested_at`, `worker_version`, `finished_at`) SELECT `id`, `created_at`, `updated_at`, `connection_id`, `mode`, `scope`, `force_reprice`, `status`, `total_count`, `processed_count`, `created_count`, `updated_count`, `price_updated_count`, `manual_skipped_count`, `hidden_count`, `deleted_count`, `error_code`, `error_context`, `started_at`, `heartbeat_at`, `current_stage`, `current_page`, `cancel_requested_at`, `worker_version`, `finished_at` FROM `supply_sync_tasks`;
-- Drop "supply_sync_tasks" table after copying rows
DROP TABLE `supply_sync_tasks`;
-- Rename temporary table "new_supply_sync_tasks" to "supply_sync_tasks"
ALTER TABLE `new_supply_sync_tasks` RENAME TO `supply_sync_tasks`;
-- Create index "supplysynctask_connection_id_created_at" to table: "supply_sync_tasks"
CREATE INDEX `supplysynctask_connection_id_created_at` ON `supply_sync_tasks` (`connection_id`, `created_at`);
-- Create index "supplysynctask_connection_id_request_key" to table: "supply_sync_tasks"
CREATE UNIQUE INDEX `supplysynctask_connection_id_request_key` ON `supply_sync_tasks` (`connection_id`, `request_key`);
-- Create index "supplysynctask_status" to table: "supply_sync_tasks"
CREATE INDEX `supplysynctask_status` ON `supply_sync_tasks` (`status`);
-- Create "new_supply_connections" table
CREATE TABLE `new_supply_connections` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `driver` text NOT NULL, `base_url` text NOT NULL, `credentials` blob NOT NULL, `status` text NOT NULL DEFAULT ('active'), `sync_task_id` integer NOT NULL DEFAULT 0, `sync_lease_token` text NOT NULL DEFAULT (''), `sync_lease_until` integer NOT NULL DEFAULT 0, `callback_url` text NULL, `retry_max` integer NOT NULL DEFAULT (5), `retry_intervals` text NOT NULL DEFAULT ('[30,60,300]'), `exchange_rate` real NOT NULL DEFAULT (1), `price_markup_percent` real NOT NULL DEFAULT (0), `price_markup_amount` integer NOT NULL DEFAULT (0), `price_rounding_mode` text NOT NULL DEFAULT ('none'), `auto_sync_price` bool NOT NULL DEFAULT (true), `stock_mode` text NOT NULL DEFAULT ('real'), `settings` json NULL, `last_ping_at` datetime NULL, `last_ping_ok` bool NOT NULL DEFAULT (false), `last_synced_at` datetime NULL, `last_error` text NULL, `balance_cache` integer NOT NULL DEFAULT (0), `last_collect_at` datetime NULL, `last_price_sync_at` datetime NULL, `last_status_sync_at` datetime NULL, `rate_state` json NULL, `rate_limit_until` datetime NULL);
-- Copy rows from old table "supply_connections" to new temporary table "new_supply_connections"
INSERT INTO `new_supply_connections` (`id`, `created_at`, `updated_at`, `name`, `driver`, `base_url`, `credentials`, `status`, `callback_url`, `retry_max`, `retry_intervals`, `exchange_rate`, `price_markup_percent`, `price_markup_amount`, `price_rounding_mode`, `auto_sync_price`, `stock_mode`, `settings`, `last_ping_at`, `last_ping_ok`, `last_synced_at`, `last_error`, `balance_cache`, `last_collect_at`, `last_price_sync_at`, `last_status_sync_at`, `rate_state`, `rate_limit_until`) SELECT `id`, `created_at`, `updated_at`, `name`, `driver`, `base_url`, `credentials`, `status`, `callback_url`, `retry_max`, `retry_intervals`, `exchange_rate`, `price_markup_percent`, `price_markup_amount`, `price_rounding_mode`, `auto_sync_price`, `stock_mode`, `settings`, `last_ping_at`, `last_ping_ok`, `last_synced_at`, `last_error`, `balance_cache`, `last_collect_at`, `last_price_sync_at`, `last_status_sync_at`, `rate_state`, `rate_limit_until` FROM `supply_connections`;
-- Drop "supply_connections" table after copying rows
DROP TABLE `supply_connections`;
-- Rename temporary table "new_supply_connections" to "supply_connections"
ALTER TABLE `new_supply_connections` RENAME TO `supply_connections`;
-- Create index "supplyconnection_status_driver" to table: "supply_connections"
CREATE INDEX `supplyconnection_status_driver` ON `supply_connections` (`status`, `driver`);
-- Create index "supplyconnection_last_synced_at" to table: "supply_connections"
CREATE INDEX `supplyconnection_last_synced_at` ON `supply_connections` (`last_synced_at`);
-- Create "supply_import_items" table
CREATE TABLE `supply_import_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `task_id` integer NOT NULL, `code` text NOT NULL, `name` text NOT NULL, `snapshot` json NOT NULL, `state` text NOT NULL DEFAULT ('pending'), `stage` text NOT NULL DEFAULT ('import'), `attempts` integer NOT NULL DEFAULT (0), `next_attempt_at` integer NOT NULL DEFAULT (0), `saved` bool NOT NULL DEFAULT (false), `created` bool NOT NULL DEFAULT (false), `activate_after_stock` bool NOT NULL DEFAULT (false), `local_product_id` integer NOT NULL DEFAULT (0), `local_revision` text NOT NULL DEFAULT (''), `error_code` text NOT NULL DEFAULT (''), `error_summary` text NOT NULL DEFAULT (''));
-- Create index "supplyimportitem_task_id_code" to table: "supply_import_items"
CREATE UNIQUE INDEX `supplyimportitem_task_id_code` ON `supply_import_items` (`task_id`, `code`);
-- Create index "supplyimportitem_task_id_state_next_attempt_at" to table: "supply_import_items"
CREATE INDEX `supplyimportitem_task_id_state_next_attempt_at` ON `supply_import_items` (`task_id`, `state`, `next_attempt_at`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
