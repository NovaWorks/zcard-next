-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_refund_orders" table
CREATE TABLE `new_refund_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `amount` integer NOT NULL, `item_allocations` json NULL, `request_key` text NULL, `request_hash` text NULL, `shipping_amount` integer NOT NULL DEFAULT (0), `fee_amount` integer NOT NULL DEFAULT (0), `channel` text NOT NULL, `status` text NOT NULL DEFAULT ('created'), `reason` text NULL, `operator_id` integer NULL, `upstream_refund_id` text NULL, `order_id` integer NOT NULL, CONSTRAINT `refund_orders_orders_refunds` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Copy rows from old table "refund_orders" to new temporary table "new_refund_orders"
INSERT INTO `new_refund_orders` (`id`, `created_at`, `updated_at`, `amount`, `item_allocations`, `shipping_amount`, `fee_amount`, `channel`, `status`, `reason`, `operator_id`, `upstream_refund_id`, `order_id`) SELECT `id`, `created_at`, `updated_at`, `amount`, `item_allocations`, `shipping_amount`, `fee_amount`, `channel`, `status`, `reason`, `operator_id`, `upstream_refund_id`, `order_id` FROM `refund_orders`;
-- Drop "refund_orders" table after copying rows
DROP TABLE `refund_orders`;
-- Rename temporary table "new_refund_orders" to "refund_orders"
ALTER TABLE `new_refund_orders` RENAME TO `refund_orders`;
-- Create index "refund_orders_request_key_key" to table: "refund_orders"
CREATE UNIQUE INDEX `refund_orders_request_key_key` ON `refund_orders` (`request_key`);
-- Create index "refundorder_order_id" to table: "refund_orders"
CREATE INDEX `refundorder_order_id` ON `refund_orders` (`order_id`);
-- Create index "refundorder_status" to table: "refund_orders"
CREATE INDEX `refundorder_status` ON `refund_orders` (`status`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
