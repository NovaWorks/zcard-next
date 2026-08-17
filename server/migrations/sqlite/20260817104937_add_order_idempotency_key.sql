-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_orders" table
CREATE TABLE `new_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `version` integer NOT NULL DEFAULT (0), `order_no` text NOT NULL, `subsite_domain` text NULL, `subsite_profit` integer NOT NULL DEFAULT (0), `profit_eligible` bool NOT NULL DEFAULT (true), `user_id` integer NULL, `guest_contact` text NULL, `query_password_hash` text NULL, `status` text NOT NULL DEFAULT ('pending_payment'), `total_amount` integer NOT NULL DEFAULT (0), `cost` integer NOT NULL DEFAULT (0), `base_currency` text NULL, `display_currency` text NULL, `exchange_rate` real NULL, `amount_display` integer NULL, `payment_channel` text NULL, `contact` text NULL, `client_ip` text NULL, `risk_ip` text NULL, `risk_flags` json NULL, `parent_id` integer NULL, `escrow_id` integer NULL, `invite_l1` integer NULL, `invite_l2` integer NULL, `invite_l3` integer NULL, `extra` json NULL, `idempotency_key` text NULL, `paid_at` datetime NULL, `closed_at` datetime NULL, `expired_at` datetime NULL);
-- Copy rows from old table "orders" to new temporary table "new_orders"
INSERT INTO `new_orders` (`id`, `created_at`, `updated_at`, `subsite_id`, `version`, `order_no`, `subsite_domain`, `subsite_profit`, `profit_eligible`, `user_id`, `guest_contact`, `query_password_hash`, `status`, `total_amount`, `cost`, `base_currency`, `display_currency`, `exchange_rate`, `amount_display`, `payment_channel`, `contact`, `client_ip`, `risk_ip`, `risk_flags`, `parent_id`, `escrow_id`, `invite_l1`, `invite_l2`, `invite_l3`, `extra`, `paid_at`, `closed_at`, `expired_at`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `version`, `order_no`, `subsite_domain`, `subsite_profit`, `profit_eligible`, `user_id`, `guest_contact`, `query_password_hash`, `status`, `total_amount`, `cost`, `base_currency`, `display_currency`, `exchange_rate`, `amount_display`, `payment_channel`, `contact`, `client_ip`, `risk_ip`, `risk_flags`, `parent_id`, `escrow_id`, `invite_l1`, `invite_l2`, `invite_l3`, `extra`, `paid_at`, `closed_at`, `expired_at` FROM `orders`;
-- Drop "orders" table after copying rows
DROP TABLE `orders`;
-- Rename temporary table "new_orders" to "orders"
ALTER TABLE `new_orders` RENAME TO `orders`;
-- Create index "orders_order_no_key" to table: "orders"
CREATE UNIQUE INDEX `orders_order_no_key` ON `orders` (`order_no`);
-- Create index "orders_idempotency_key_key" to table: "orders"
CREATE UNIQUE INDEX `orders_idempotency_key_key` ON `orders` (`idempotency_key`);
-- Create index "order_subsite_id_created_at" to table: "orders"
CREATE INDEX `order_subsite_id_created_at` ON `orders` (`subsite_id`, `created_at`);
-- Create index "order_user_id_status" to table: "orders"
CREATE INDEX `order_user_id_status` ON `orders` (`user_id`, `status`);
-- Create index "order_status_expired_at" to table: "orders"
CREATE INDEX `order_status_expired_at` ON `orders` (`status`, `expired_at`);
-- Create index "order_parent_id" to table: "orders"
CREATE INDEX `order_parent_id` ON `orders` (`parent_id`);
-- Create index "order_escrow_id" to table: "orders"
CREATE INDEX `order_escrow_id` ON `orders` (`escrow_id`);
-- Create index "order_invite_l1" to table: "orders"
CREATE INDEX `order_invite_l1` ON `orders` (`invite_l1`);
-- Create index "order_risk_ip_user_id_status" to table: "orders"
CREATE INDEX `order_risk_ip_user_id_status` ON `orders` (`risk_ip`, `user_id`, `status`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
