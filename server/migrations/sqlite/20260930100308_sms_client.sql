-- Add column "delivery_kind" to table: "products"
ALTER TABLE `products` ADD COLUMN `delivery_kind` text NOT NULL DEFAULT 'card';
-- Add column "sms_product" to table: "products"
ALTER TABLE `products` ADD COLUMN `sms_product` json NULL;
-- Add column "delivery_kind" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `delivery_kind` text NOT NULL DEFAULT 'card';
-- Add column "sms_product" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `sms_product` json NULL;
-- Add column "sms_purchase_snapshot" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `sms_purchase_snapshot` text NULL;
-- Create "sms_intents" table
CREATE TABLE `sms_intents` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `order_id` integer NOT NULL, `order_item_id` integer NOT NULL, `user_id` integer NOT NULL, `connection_id` integer NOT NULL, `connection_identity` text NOT NULL, `request_no` text NOT NULL, `request_json` text NOT NULL, `request_hash` text NOT NULL, `upstream_order_id` text NULL, `phase` text NOT NULL DEFAULT ('purchase'), `state` text NOT NULL DEFAULT ('allocating'), `session_id` text NOT NULL DEFAULT (''), `version` integer NOT NULL DEFAULT (0), `sms_revision` integer NOT NULL DEFAULT (0), `snapshot_cipher` blob NULL, `received` bool NOT NULL DEFAULT (false), `can_cancel` bool NOT NULL DEFAULT (false), `can_finish` bool NOT NULL DEFAULT (false), `charged_amount` integer NOT NULL DEFAULT (0), `settlement_state` text NOT NULL DEFAULT (''), `refunded_amount` integer NOT NULL DEFAULT (0), `refund_reference` text NOT NULL DEFAULT (''), `rejected_receipt` bool NOT NULL DEFAULT (false), `retail_refund_state` text NOT NULL DEFAULT ('none'), `refund_id` integer NOT NULL DEFAULT (0), `next_run_at` integer NOT NULL DEFAULT (0), `lease_until` integer NOT NULL DEFAULT (0), `lease_token` text NOT NULL DEFAULT (''), `attempts` integer NOT NULL DEFAULT (0), `last_error` text NOT NULL DEFAULT (''));
-- Create index "smsintent_order_item_id" to table: "sms_intents"
CREATE UNIQUE INDEX `smsintent_order_item_id` ON `sms_intents` (`order_item_id`);
-- Create index "smsintent_request_no" to table: "sms_intents"
CREATE UNIQUE INDEX `smsintent_request_no` ON `sms_intents` (`request_no`);
-- Create index "smsintent_connection_id_upstream_order_id" to table: "sms_intents"
CREATE UNIQUE INDEX `smsintent_connection_id_upstream_order_id` ON `sms_intents` (`connection_id`, `upstream_order_id`);
-- Create index "smsintent_phase_next_run_at_lease_until" to table: "sms_intents"
CREATE INDEX `smsintent_phase_next_run_at_lease_until` ON `sms_intents` (`phase`, `next_run_at`, `lease_until`);
-- Create index "smsintent_subsite_id_user_id_order_id" to table: "sms_intents"
CREATE INDEX `smsintent_subsite_id_user_id_order_id` ON `sms_intents` (`subsite_id`, `user_id`, `order_id`);
-- Create "sms_operations" table
CREATE TABLE `sms_operations` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `intent_id` integer NOT NULL, `operation_id` text NOT NULL, `action` text NOT NULL, `status` text NOT NULL DEFAULT ('pending'), `error_code` text NOT NULL DEFAULT (''), `attempts` integer NOT NULL DEFAULT (0));
-- Create index "smsoperation_operation_id" to table: "sms_operations"
CREATE UNIQUE INDEX `smsoperation_operation_id` ON `sms_operations` (`operation_id`);
-- Create index "smsoperation_intent_id_status" to table: "sms_operations"
CREATE INDEX `smsoperation_intent_id_status` ON `sms_operations` (`intent_id`, `status`);
