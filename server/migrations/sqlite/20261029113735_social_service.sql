-- Add column "social_schema" to table: "products"
ALTER TABLE `products` ADD COLUMN `social_schema` json NULL;
-- Add column "social_purchase_snapshot" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `social_purchase_snapshot` text NOT NULL DEFAULT '';
-- Add column "social_schema" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `social_schema` json NULL;
-- Create "social_operations" table
CREATE TABLE `social_operations` (`id` text NOT NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `task_id` text NOT NULL, `operation_id` text NOT NULL, `action` text NOT NULL, `status` text NOT NULL DEFAULT ('pending'), `upstream_operation_id` text NOT NULL DEFAULT (''), `submit_started` bool NOT NULL DEFAULT (false), `amount_cents` integer NOT NULL DEFAULT (0), `reference` text NOT NULL DEFAULT (''), `operator_id` integer NOT NULL DEFAULT (0), `reason` text NOT NULL DEFAULT (''), `attempts` integer NOT NULL DEFAULT (0), `error_code` text NOT NULL DEFAULT (''), PRIMARY KEY (`id`));
-- Create index "socialoperation_task_id_operation_id" to table: "social_operations"
CREATE UNIQUE INDEX `socialoperation_task_id_operation_id` ON `social_operations` (`task_id`, `operation_id`);
-- Create index "socialoperation_task_id_status" to table: "social_operations"
CREATE INDEX `socialoperation_task_id_status` ON `social_operations` (`task_id`, `status`);
-- Create "social_quotes" table
CREATE TABLE `social_quotes` (`id` text NOT NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `role` text NOT NULL DEFAULT ('wholesale'), `account_id` integer NOT NULL DEFAULT (0), `user_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `connection_id` integer NOT NULL, `connection_identity` text NOT NULL DEFAULT (''), `connection_revision` text NOT NULL DEFAULT (''), `product_revision` integer NOT NULL DEFAULT (0), `schema_revision` text NOT NULL, `pricing_revision` text NOT NULL DEFAULT (''), `upstream_product_id` text NOT NULL, `params_json` text NOT NULL DEFAULT (''), `params_cipher` blob NULL, `schema_json` text NOT NULL DEFAULT (''), `supply_quote_id` text NOT NULL DEFAULT (''), `currency` text NOT NULL DEFAULT ('CNY'), `amount_cents` integer NOT NULL, `cost_cents` integer NOT NULL DEFAULT (0), `expires_at` integer NOT NULL, `consumed_by` text NOT NULL DEFAULT (''), PRIMARY KEY (`id`));
-- Create index "socialquote_role_account_id_user_id_product_id" to table: "social_quotes"
CREATE INDEX `socialquote_role_account_id_user_id_product_id` ON `social_quotes` (`role`, `account_id`, `user_id`, `product_id`);
-- Create index "socialquote_expires_at" to table: "social_quotes"
CREATE INDEX `socialquote_expires_at` ON `social_quotes` (`expires_at`);
-- Create "social_tasks" table
CREATE TABLE `social_tasks` (`id` text NOT NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `role` text NOT NULL DEFAULT ('wholesale'), `supply_order_id` integer NOT NULL DEFAULT (0), `order_id` integer NOT NULL DEFAULT (0), `order_item_id` integer NOT NULL DEFAULT (0), `account_id` integer NOT NULL DEFAULT (0), `user_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL DEFAULT (0), `connection_id` integer NOT NULL, `connection_identity` text NOT NULL DEFAULT (''), `connection_revision` text NOT NULL DEFAULT (''), `quote_id` text NOT NULL, `schema_revision` text NOT NULL, `request_no` text NOT NULL, `request_hash` text NOT NULL, `request_json` text NOT NULL DEFAULT (''), `request_cipher` blob NULL, `upstream_product_id` text NOT NULL, `upstream_order_id` text NOT NULL DEFAULT (''), `upstream_operation_id` text NOT NULL DEFAULT (''), `phase` text NOT NULL DEFAULT ('purchase'), `state` text NOT NULL DEFAULT ('pending'), `submit_started` bool NOT NULL DEFAULT (false), `submit_started_at` integer NOT NULL DEFAULT (0), `version` integer NOT NULL DEFAULT (1), `upstream_version` integer NOT NULL DEFAULT (0), `snapshot_cipher` blob NULL, `currency` text NOT NULL DEFAULT ('CNY'), `charged_amount` integer NOT NULL DEFAULT (0), `cost_cents` integer NOT NULL DEFAULT (0), `settlement_state` text NOT NULL DEFAULT ('paid'), `refunded_amount` integer NOT NULL DEFAULT (0), `refund_reference` text NOT NULL DEFAULT (''), `retail_paid_amount` integer NOT NULL DEFAULT (0), `retail_refunded_amount` integer NOT NULL DEFAULT (0), `retail_refund_state` text NOT NULL DEFAULT ('none'), `refund_id` integer NOT NULL DEFAULT (0), `can_cancel` bool NOT NULL DEFAULT (false), `can_refill` bool NOT NULL DEFAULT (false), `next_run_at` integer NOT NULL DEFAULT (0), `lease_until` integer NOT NULL DEFAULT (0), `lease_token` text NOT NULL DEFAULT (''), `attempts` integer NOT NULL DEFAULT (0), `last_error` text NOT NULL DEFAULT (''), PRIMARY KEY (`id`));
-- Create index "socialtask_role_account_id_request_no" to table: "social_tasks"
CREATE UNIQUE INDEX `socialtask_role_account_id_request_no` ON `social_tasks` (`role`, `account_id`, `request_no`);
-- Create index "socialtask_role_supply_order_id" to table: "social_tasks"
CREATE INDEX `socialtask_role_supply_order_id` ON `social_tasks` (`role`, `supply_order_id`);
-- Create index "socialtask_role_order_item_id" to table: "social_tasks"
CREATE INDEX `socialtask_role_order_item_id` ON `social_tasks` (`role`, `order_item_id`);
-- Create index "socialtask_connection_id_upstream_order_id" to table: "social_tasks"
CREATE INDEX `socialtask_connection_id_upstream_order_id` ON `social_tasks` (`connection_id`, `upstream_order_id`);
-- Create index "socialtask_role_phase_next_run_at_lease_until" to table: "social_tasks"
CREATE INDEX `socialtask_role_phase_next_run_at_lease_until` ON `social_tasks` (`role`, `phase`, `next_run_at`, `lease_until`);
-- Create index "socialtask_subsite_id_user_id_order_id" to table: "social_tasks"
CREATE INDEX `socialtask_subsite_id_user_id_order_id` ON `social_tasks` (`subsite_id`, `user_id`, `order_id`);
-- Create index "socialtask_account_id" to table: "social_tasks"
CREATE INDEX `socialtask_account_id` ON `social_tasks` (`account_id`);
