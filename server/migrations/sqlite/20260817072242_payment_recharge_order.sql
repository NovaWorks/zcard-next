-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_payments" table
CREATE TABLE `new_payments` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `recharge_order_id` integer NULL, `channel` text NOT NULL, `channel_order_no` text NULL, `amount` integer NOT NULL, `charged_amount` integer NOT NULL DEFAULT (0), `fee` integer NOT NULL DEFAULT (0), `status` text NOT NULL DEFAULT ('pending'), `paid_at` datetime NULL, `raw` json NULL, `idempotency_key` text NULL, `order_id` integer NULL, CONSTRAINT `payments_orders_payments` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE SET NULL);
-- Copy rows from old table "payments" to new temporary table "new_payments"
INSERT INTO `new_payments` (`id`, `created_at`, `updated_at`, `subsite_id`, `channel`, `channel_order_no`, `amount`, `charged_amount`, `fee`, `status`, `paid_at`, `raw`, `idempotency_key`, `order_id`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `channel`, `channel_order_no`, `amount`, `charged_amount`, `fee`, `status`, `paid_at`, `raw`, `idempotency_key`, `order_id` FROM `payments`;
-- Drop "payments" table after copying rows
DROP TABLE `payments`;
-- Rename temporary table "new_payments" to "payments"
ALTER TABLE `new_payments` RENAME TO `payments`;
-- Create index "payment_order_id" to table: "payments"
CREATE INDEX `payment_order_id` ON `payments` (`order_id`);
-- Create index "payment_channel_channel_order_no" to table: "payments"
CREATE UNIQUE INDEX `payment_channel_channel_order_no` ON `payments` (`channel`, `channel_order_no`);
-- Create index "payment_status_created_at" to table: "payments"
CREATE INDEX `payment_status_created_at` ON `payments` (`status`, `created_at`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
