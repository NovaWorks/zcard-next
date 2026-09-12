-- Create "lottery_accounts" table
CREATE TABLE `lottery_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `activity_id` integer NOT NULL, `user_id` integer NOT NULL, `period` text NOT NULL, `balance` integer NOT NULL DEFAULT (0), `auto_granted` bool NOT NULL DEFAULT (false), `version` integer NOT NULL DEFAULT (0));
-- Create index "lotteryaccount_subsite_id_activity_id_user_id_period" to table: "lottery_accounts"
CREATE UNIQUE INDEX `lotteryaccount_subsite_id_activity_id_user_id_period` ON `lottery_accounts` (`subsite_id`, `activity_id`, `user_id`, `period`);
-- Create "lottery_activities" table
CREATE TABLE `lottery_activities` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `description` text NOT NULL DEFAULT (''), `image` text NOT NULL DEFAULT (''), `status` text NOT NULL DEFAULT ('draft'), `start_at` datetime NOT NULL, `end_at` datetime NOT NULL, `timezone` text NOT NULL DEFAULT ('Asia/Shanghai'), `chance_mode` text NOT NULL DEFAULT ('once'), `chance_count` integer NOT NULL DEFAULT (3), `revision` integer NOT NULL DEFAULT (1), `published` bool NOT NULL DEFAULT (false));
-- Create index "lotteryactivity_subsite_id_status" to table: "lottery_activities"
CREATE INDEX `lotteryactivity_subsite_id_status` ON `lottery_activities` (`subsite_id`, `status`);
-- Create "lottery_chance_logs" table
CREATE TABLE `lottery_chance_logs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `activity_id` integer NOT NULL, `user_id` integer NOT NULL, `period` text NOT NULL, `amount` integer NOT NULL, `kind` text NOT NULL, `request_key` text NOT NULL, `remark` text NOT NULL DEFAULT (''), `admin_id` integer NOT NULL DEFAULT (0));
-- Create index "lotterychancelog_subsite_id_activity_id_user_id_request_key" to table: "lottery_chance_logs"
CREATE UNIQUE INDEX `lotterychancelog_subsite_id_activity_id_user_id_request_key` ON `lottery_chance_logs` (`subsite_id`, `activity_id`, `user_id`, `request_key`);
-- Create "lottery_draws" table
CREATE TABLE `lottery_draws` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `activity_id` integer NOT NULL, `user_id` integer NOT NULL, `draw_no` text NOT NULL, `request_key` text NOT NULL, `activity_name` text NOT NULL, `prize_name` text NOT NULL DEFAULT (''), `mode` text NOT NULL DEFAULT (''), `status` text NOT NULL, `prize_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL DEFAULT (0), `sku_id` integer NOT NULL DEFAULT (0), `card_id` integer NULL, `cost` integer NOT NULL DEFAULT (0), `revision` integer NOT NULL, `rule_snapshot` json NULL, `content` blob NULL, `delivered_at` datetime NULL, `admin_id` integer NOT NULL DEFAULT (0), `remark` text NOT NULL DEFAULT (''));
-- Create index "lottery_draws_draw_no_key" to table: "lottery_draws"
CREATE UNIQUE INDEX `lottery_draws_draw_no_key` ON `lottery_draws` (`draw_no`);
-- Create index "lotterydraw_subsite_id_activity_id_user_id_request_key" to table: "lottery_draws"
CREATE UNIQUE INDEX `lotterydraw_subsite_id_activity_id_user_id_request_key` ON `lottery_draws` (`subsite_id`, `activity_id`, `user_id`, `request_key`);
-- Create index "lotterydraw_card_id" to table: "lottery_draws"
CREATE UNIQUE INDEX `lotterydraw_card_id` ON `lottery_draws` (`card_id`);
-- Create index "lotterydraw_subsite_id_user_id_id" to table: "lottery_draws"
CREATE INDEX `lotterydraw_subsite_id_user_id_id` ON `lottery_draws` (`subsite_id`, `user_id`, `id`);
-- Create index "lotterydraw_subsite_id_activity_id_status" to table: "lottery_draws"
CREATE INDEX `lotterydraw_subsite_id_activity_id_status` ON `lottery_draws` (`subsite_id`, `activity_id`, `status`);
-- Create index "lotterydraw_product_id_sku_id" to table: "lottery_draws"
CREATE INDEX `lotterydraw_product_id_sku_id` ON `lottery_draws` (`product_id`, `sku_id`);
-- Create "lottery_prizes" table
CREATE TABLE `lottery_prizes` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `activity_id` integer NOT NULL, `name` text NOT NULL, `image` text NOT NULL DEFAULT (''), `mode` text NOT NULL, `product_id` integer NOT NULL DEFAULT (0), `sku_id` integer NOT NULL DEFAULT (0), `probability` integer NOT NULL DEFAULT (0), `quantity` integer NOT NULL DEFAULT (1), `issued` integer NOT NULL DEFAULT (0), `sort` integer NOT NULL DEFAULT (0), `enabled` bool NOT NULL DEFAULT (true), `content` blob NULL);
-- Create index "lotteryprize_subsite_id_activity_id_enabled" to table: "lottery_prizes"
CREATE INDEX `lotteryprize_subsite_id_activity_id_enabled` ON `lottery_prizes` (`subsite_id`, `activity_id`, `enabled`);
-- Create index "lotteryprize_product_id_sku_id" to table: "lottery_prizes"
CREATE INDEX `lotteryprize_product_id_sku_id` ON `lottery_prizes` (`product_id`, `sku_id`);
-- Create "lottery_revisions" table
CREATE TABLE `lottery_revisions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `activity_id` integer NOT NULL, `revision` integer NOT NULL, `admin_id` integer NOT NULL, `snapshot` json NOT NULL);
-- Create index "lotteryrevision_subsite_id_activity_id_revision" to table: "lottery_revisions"
CREATE UNIQUE INDEX `lotteryrevision_subsite_id_activity_id_revision` ON `lottery_revisions` (`subsite_id`, `activity_id`, `revision`);
