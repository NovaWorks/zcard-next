-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_supplier_accounts" table
CREATE TABLE `new_supplier_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `api_key` text NOT NULL, `api_secret` blob NOT NULL, `contact` text NULL, `status` text NOT NULL DEFAULT ('applying'), `balance_cache` integer NOT NULL DEFAULT (0), `notify_url` text NULL, `reviewed_at` datetime NULL, `protocol` text NOT NULL DEFAULT ('zcard'), `display_name` text NULL, `owner_user_id` integer NULL DEFAULT (0), `apply_reason` text NULL, `review_note` text NULL);
-- Copy rows from old table "supplier_accounts" to new temporary table "new_supplier_accounts"
INSERT INTO `new_supplier_accounts` (`id`, `created_at`, `updated_at`, `name`, `api_key`, `api_secret`, `contact`, `status`, `balance_cache`, `notify_url`, `reviewed_at`, `protocol`, `display_name`) SELECT `id`, `created_at`, `updated_at`, `name`, `api_key`, `api_secret`, `contact`, `status`, `balance_cache`, `notify_url`, `reviewed_at`, `protocol`, `display_name` FROM `supplier_accounts`;
-- Drop "supplier_accounts" table after copying rows
DROP TABLE `supplier_accounts`;
-- Rename temporary table "new_supplier_accounts" to "supplier_accounts"
ALTER TABLE `new_supplier_accounts` RENAME TO `supplier_accounts`;
-- Create index "supplier_accounts_api_key_key" to table: "supplier_accounts"
CREATE UNIQUE INDEX `supplier_accounts_api_key_key` ON `supplier_accounts` (`api_key`);
-- Create index "supplieraccount_owner_user_id" to table: "supplier_accounts"
CREATE INDEX `supplieraccount_owner_user_id` ON `supplier_accounts` (`owner_user_id`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
