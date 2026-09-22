-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_payment_channels" table
CREATE TABLE `new_payment_channels` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `code` text NOT NULL, `driver` text NOT NULL, `config` blob NOT NULL, `fee` integer NOT NULL DEFAULT (0), `fee_type` text NOT NULL DEFAULT ('fixed'), `fee_bearer` text NOT NULL DEFAULT ('merchant'), `recommended` bool NOT NULL DEFAULT (false), `recommend_label` text NOT NULL DEFAULT (''), `recommend_description` text NOT NULL DEFAULT (''), `sort` integer NOT NULL DEFAULT (0), `enabled` bool NOT NULL DEFAULT (true), `deleted_at` datetime NULL, `allow_purchase` bool NOT NULL DEFAULT (true), `allow_member_recharge` bool NOT NULL DEFAULT (true), `allow_supply_recharge` bool NOT NULL DEFAULT (true), `icon` text NOT NULL DEFAULT (''), `methods` json NULL);
-- Copy rows from old table "payment_channels" to new temporary table "new_payment_channels"
INSERT INTO `new_payment_channels` (`id`, `created_at`, `updated_at`, `subsite_id`, `name`, `code`, `driver`, `config`, `fee`, `fee_type`, `fee_bearer`, `sort`, `enabled`, `deleted_at`, `allow_purchase`, `allow_member_recharge`, `allow_supply_recharge`, `icon`, `methods`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `name`, `code`, `driver`, `config`, `fee`, `fee_type`, `fee_bearer`, `sort`, `enabled`, `deleted_at`, `allow_purchase`, `allow_member_recharge`, `allow_supply_recharge`, `icon`, `methods` FROM `payment_channels`;
-- Drop "payment_channels" table after copying rows
DROP TABLE `payment_channels`;
-- Rename temporary table "new_payment_channels" to "payment_channels"
ALTER TABLE `new_payment_channels` RENAME TO `payment_channels`;
-- Create index "paymentchannel_subsite_id_code" to table: "payment_channels"
CREATE UNIQUE INDEX `paymentchannel_subsite_id_code` ON `payment_channels` (`subsite_id`, `code`);
-- Create "new_refund_orders" table
CREATE TABLE `new_refund_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `amount` integer NOT NULL, `fee_amount` integer NOT NULL DEFAULT (0), `channel` text NOT NULL, `status` text NOT NULL DEFAULT ('created'), `reason` text NULL, `operator_id` integer NULL, `upstream_refund_id` text NULL, `order_id` integer NOT NULL, CONSTRAINT `refund_orders_orders_refunds` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Copy rows from old table "refund_orders" to new temporary table "new_refund_orders"
INSERT INTO `new_refund_orders` (`id`, `created_at`, `updated_at`, `amount`, `channel`, `status`, `reason`, `operator_id`, `upstream_refund_id`, `order_id`) SELECT `id`, `created_at`, `updated_at`, `amount`, `channel`, `status`, `reason`, `operator_id`, `upstream_refund_id`, `order_id` FROM `refund_orders`;
-- Drop "refund_orders" table after copying rows
DROP TABLE `refund_orders`;
-- Rename temporary table "new_refund_orders" to "refund_orders"
ALTER TABLE `new_refund_orders` RENAME TO `refund_orders`;
-- Create index "refundorder_order_id" to table: "refund_orders"
CREATE INDEX `refundorder_order_id` ON `refund_orders` (`order_id`);
-- Create index "refundorder_status" to table: "refund_orders"
CREATE INDEX `refundorder_status` ON `refund_orders` (`status`);
-- Add column "pricing_snapshot" to table: "payments"
ALTER TABLE `payments` ADD COLUMN `pricing_snapshot` json NULL;
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
