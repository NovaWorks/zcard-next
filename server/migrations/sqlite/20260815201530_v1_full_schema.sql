-- Create "affiliate_commissions" table
CREATE TABLE `affiliate_commissions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `order_id` integer NOT NULL, `buyer_id` integer NOT NULL, `referrer_id` integer NOT NULL, `tier` integer NOT NULL, `rate` real NOT NULL, `base_amount` integer NOT NULL, `amount` integer NOT NULL, `status` text NOT NULL DEFAULT ('pending_confirm'), `available_at` datetime NULL);
-- Create index "affiliatecommission_order_id_tier" to table: "affiliate_commissions"
CREATE UNIQUE INDEX `affiliatecommission_order_id_tier` ON `affiliate_commissions` (`order_id`, `tier`);
-- Create index "affiliatecommission_referrer_id_status" to table: "affiliate_commissions"
CREATE INDEX `affiliatecommission_referrer_id_status` ON `affiliate_commissions` (`referrer_id`, `status`);
-- Create index "affiliatecommission_available_at" to table: "affiliate_commissions"
CREATE INDEX `affiliatecommission_available_at` ON `affiliate_commissions` (`available_at`);
-- Create "audit_logs" table
CREATE TABLE `audit_logs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `operator_type` text NOT NULL, `operator_id` integer NULL, `permission_point` text NULL, `action` text NOT NULL, `route` text NOT NULL, `before` json NULL, `after` json NULL, `ip` text NULL, `user_agent` text NULL, `created_at` datetime NOT NULL);
-- Create index "auditlog_operator_type_operator_id_created_at" to table: "audit_logs"
CREATE INDEX `auditlog_operator_type_operator_id_created_at` ON `audit_logs` (`operator_type`, `operator_id`, `created_at`);
-- Create index "auditlog_created_at" to table: "audit_logs"
CREATE INDEX `auditlog_created_at` ON `audit_logs` (`created_at`);
-- Create "banners" table
CREATE TABLE `banners` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `position` text NOT NULL DEFAULT ('top'), `title_json` json NULL, `image` text NOT NULL, `mobile_image` text NULL, `link_type` text NOT NULL DEFAULT ('url'), `link_value` text NULL, `is_active` bool NOT NULL DEFAULT (true), `start_at` datetime NULL, `end_at` datetime NULL, `sort` integer NOT NULL DEFAULT (0));
-- Create index "banner_subsite_id_position_is_active" to table: "banners"
CREATE INDEX `banner_subsite_id_position_is_active` ON `banners` (`subsite_id`, `position`, `is_active`);
-- Create "card_imports" table
CREATE TABLE `card_imports` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `filename` text NOT NULL, `total` integer NOT NULL DEFAULT (0), `imported` integer NOT NULL DEFAULT (0), `skipped` integer NOT NULL DEFAULT (0), `failed` integer NOT NULL DEFAULT (0), `status` text NOT NULL DEFAULT ('pending'), `operator_id` integer NULL);
-- Create index "cardimport_product_id_created_at" to table: "card_imports"
CREATE INDEX `cardimport_product_id_created_at` ON `card_imports` (`product_id`, `created_at`);
-- Create "cart_items" table
CREATE TABLE `cart_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `user_id` integer NOT NULL, `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `quantity` integer NOT NULL DEFAULT (1), `fulfillment_type` text NOT NULL DEFAULT ('auto'));
-- Create index "cartitem_user_id_product_id_sku_id" to table: "cart_items"
CREATE UNIQUE INDEX `cartitem_user_id_product_id_sku_id` ON `cart_items` (`user_id`, `product_id`, `sku_id`);
-- Create "categories" table
CREATE TABLE `categories` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `parent_id` integer NULL, `name` text NOT NULL, `icon` text NULL, `hide` bool NOT NULL DEFAULT (false), `sort` integer NOT NULL DEFAULT (0), `visible_subsites` json NULL);
-- Create index "category_subsite_id_parent_id" to table: "categories"
CREATE INDEX `category_subsite_id_parent_id` ON `categories` (`subsite_id`, `parent_id`);
-- Create "coupons" table
CREATE TABLE `coupons` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `batch_id` text NOT NULL DEFAULT (''), `name` text NOT NULL, `type` text NOT NULL, `value` integer NOT NULL, `code` text NOT NULL, `scope` json NULL, `expire_at` datetime NULL, `per_user_limit` integer NOT NULL DEFAULT (1), `status` text NOT NULL DEFAULT ('unused'), `user_id` integer NULL, `used_at` datetime NULL, `used_order_id` integer NULL);
-- Create index "coupons_code_key" to table: "coupons"
CREATE UNIQUE INDEX `coupons_code_key` ON `coupons` (`code`);
-- Create index "coupon_batch_id" to table: "coupons"
CREATE INDEX `coupon_batch_id` ON `coupons` (`batch_id`);
-- Create index "coupon_user_id_status" to table: "coupons"
CREATE INDEX `coupon_user_id_status` ON `coupons` (`user_id`, `status`);
-- Create "daily_stats" table
CREATE TABLE `daily_stats` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `subsite_id` integer NOT NULL DEFAULT (0), `stat_date` text NOT NULL, `metric` text NOT NULL, `dimension_key` text NOT NULL DEFAULT (''), `value` integer NOT NULL DEFAULT (0), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL);
-- Create index "dailystat_subsite_id_stat_date_metric_dimension_key" to table: "daily_stats"
CREATE UNIQUE INDEX `dailystat_subsite_id_stat_date_metric_dimension_key` ON `daily_stats` (`subsite_id`, `stat_date`, `metric`, `dimension_key`);
-- Create "downstream_callbacks" table
CREATE TABLE `downstream_callbacks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `supply_order_id` integer NOT NULL, `account_id` integer NOT NULL, `downstream_order_no` text NOT NULL, `callback_url` text NOT NULL, `trace_id` text NULL, `callback_status` text NOT NULL DEFAULT ('pending'), `retry_count` integer NOT NULL DEFAULT (0), `last_callback_at` datetime NULL, `last_error` text NULL);
-- Create index "downstream_callbacks_supply_order_id_key" to table: "downstream_callbacks"
CREATE UNIQUE INDEX `downstream_callbacks_supply_order_id_key` ON `downstream_callbacks` (`supply_order_id`);
-- Create index "downstreamcallback_callback_status_retry_count" to table: "downstream_callbacks"
CREATE INDEX `downstreamcallback_callback_status_retry_count` ON `downstream_callbacks` (`callback_status`, `retry_count`);
-- Create "email_verifications" table
CREATE TABLE `email_verifications` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `email` text NOT NULL, `user_id` integer NULL, `purpose` text NOT NULL, `code_hash` text NOT NULL, `expires_at` datetime NOT NULL, `verified_at` datetime NULL, `attempt_count` integer NOT NULL DEFAULT (0), `created_at` datetime NOT NULL);
-- Create index "emailverification_email_purpose_created_at" to table: "email_verifications"
CREATE INDEX `emailverification_email_purpose_created_at` ON `email_verifications` (`email`, `purpose`, `created_at`);
-- Create "external_identities" table
CREATE TABLE `external_identities` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `user_id` integer NOT NULL, `provider` text NOT NULL, `provider_user_id` text NOT NULL, `username` text NULL, `avatar_url` text NULL, `auth_at` datetime NULL);
-- Create index "externalidentity_provider_provider_user_id" to table: "external_identities"
CREATE UNIQUE INDEX `externalidentity_provider_provider_user_id` ON `external_identities` (`provider`, `provider_user_id`);
-- Create index "externalidentity_user_id" to table: "external_identities"
CREATE INDEX `externalidentity_user_id` ON `external_identities` (`user_id`);
-- Create "flash_sales" table
CREATE TABLE `flash_sales` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `flash_price` integer NOT NULL, `start_at` datetime NOT NULL, `end_at` datetime NOT NULL, `limit_qty` integer NOT NULL, `sold_qty` integer NOT NULL DEFAULT (0), `per_user_limit` integer NOT NULL DEFAULT (1));
-- Create index "flashsale_product_id_sku_id" to table: "flash_sales"
CREATE INDEX `flashsale_product_id_sku_id` ON `flash_sales` (`product_id`, `sku_id`);
-- Create index "flashsale_end_at" to table: "flash_sales"
CREATE INDEX `flashsale_end_at` ON `flash_sales` (`end_at`);
-- Create "giftcards" table
CREATE TABLE `giftcards` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `batch_id` integer NOT NULL, `code` blob NOT NULL, `code_hash` text NOT NULL, `amount` integer NOT NULL, `currency` text NOT NULL DEFAULT ('CNY'), `status` text NOT NULL DEFAULT ('unused'), `used_by` integer NULL, `used_at` datetime NULL, `expires_at` datetime NULL);
-- Create index "giftcards_code_hash_key" to table: "giftcards"
CREATE UNIQUE INDEX `giftcards_code_hash_key` ON `giftcards` (`code_hash`);
-- Create index "giftcard_batch_id" to table: "giftcards"
CREATE INDEX `giftcard_batch_id` ON `giftcards` (`batch_id`);
-- Create index "giftcard_status" to table: "giftcards"
CREATE INDEX `giftcard_status` ON `giftcards` (`status`);
-- Create "giftcard_batches" table
CREATE TABLE `giftcard_batches` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `batch_no` text NOT NULL, `name` text NOT NULL, `amount` integer NOT NULL, `quantity` integer NOT NULL, `operator_id` integer NULL);
-- Create index "giftcard_batches_batch_no_key" to table: "giftcard_batches"
CREATE UNIQUE INDEX `giftcard_batches_batch_no_key` ON `giftcard_batches` (`batch_no`);
-- Create "media" table
CREATE TABLE `media` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `category_id` integer NULL, `path` text NOT NULL, `name` text NOT NULL, `mime` text NOT NULL, `size` integer NOT NULL, `width` integer NULL, `height` integer NULL, `sha256` text NULL, `storage` text NOT NULL DEFAULT ('local'), `ref_count` integer NOT NULL DEFAULT (0), `uploader_id` integer NULL);
-- Create index "media_category_id" to table: "media"
CREATE INDEX `media_category_id` ON `media` (`category_id`);
-- Create index "media_sha256" to table: "media"
CREATE INDEX `media_sha256` ON `media` (`sha256`);
-- Create "media_categories" table
CREATE TABLE `media_categories` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `parent_id` integer NULL, `name` text NOT NULL, `sort` integer NOT NULL DEFAULT (0));
-- Create "member_levels" table
CREATE TABLE `member_levels` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `logo` text NULL, `badge_color` text NULL, `threshold_type` text NOT NULL DEFAULT ('recharge'), `threshold_recharge` integer NOT NULL DEFAULT (0), `threshold_consume` integer NOT NULL DEFAULT (0), `discount` integer NOT NULL DEFAULT (0), `points_rule` json NULL, `sort` integer NOT NULL DEFAULT (0), `enabled` bool NOT NULL DEFAULT (true));
-- Create "member_product_groups" table
CREATE TABLE `member_product_groups` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `product_ids` json NOT NULL, `discount` integer NOT NULL DEFAULT (0), `stack_member` bool NOT NULL DEFAULT (false), `stack_coupon` bool NOT NULL DEFAULT (false), `badge_style` text NULL);
-- Create "notifications" table
CREATE TABLE `notifications` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `user_id` integer NOT NULL, `title` text NOT NULL, `content` text NOT NULL, `read_at` datetime NULL, `source_type` text NULL, `source_id` integer NULL, `created_at` datetime NOT NULL);
-- Create index "notification_user_id_read_at_created_at" to table: "notifications"
CREATE INDEX `notification_user_id_read_at_created_at` ON `notifications` (`user_id`, `read_at`, `created_at`);
-- Create "notification_logs" table
CREATE TABLE `notification_logs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `event_type` text NOT NULL, `biz_type` text NULL, `biz_id` integer NULL, `channel` text NOT NULL, `recipient` text NOT NULL, `locale` text NOT NULL DEFAULT ('zh_CN'), `subject` text NULL, `body` text NULL, `status` text NOT NULL DEFAULT ('pending'), `error_message` text NULL, `variables` json NULL, `created_at` datetime NOT NULL);
-- Create index "notificationlog_status_created_at" to table: "notification_logs"
CREATE INDEX `notificationlog_status_created_at` ON `notification_logs` (`status`, `created_at`);
-- Create index "notificationlog_event_type_created_at" to table: "notification_logs"
CREATE INDEX `notificationlog_event_type_created_at` ON `notification_logs` (`event_type`, `created_at`);
-- Create "notify_templates" table
CREATE TABLE `notify_templates` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `event_type` text NOT NULL, `channel` text NOT NULL, `locale` text NOT NULL DEFAULT ('zh_CN'), `subject_tpl` text NOT NULL, `body_tpl` text NOT NULL, `enabled` bool NOT NULL DEFAULT (true));
-- Create index "notifytemplate_event_type_channel_locale" to table: "notify_templates"
CREATE UNIQUE INDEX `notifytemplate_event_type_channel_locale` ON `notify_templates` (`event_type`, `channel`, `locale`);
-- Create "point_accounts" table
CREATE TABLE `point_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `user_id` integer NOT NULL, `balance` integer NOT NULL DEFAULT (0), `version` integer NOT NULL DEFAULT (0), `updated_at` datetime NOT NULL);
-- Create index "point_accounts_user_id_key" to table: "point_accounts"
CREATE UNIQUE INDEX `point_accounts_user_id_key` ON `point_accounts` (`user_id`);
-- Create "point_transactions" table
CREATE TABLE `point_transactions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `user_id` integer NOT NULL, `direction` text NOT NULL, `type` text NOT NULL, `amount` integer NOT NULL, `balance_before` integer NOT NULL, `balance_after` integer NOT NULL, `reference` text NOT NULL, `order_id` integer NULL, `remark` text NULL, `created_at` datetime NOT NULL);
-- Create index "point_transactions_reference_key" to table: "point_transactions"
CREATE UNIQUE INDEX `point_transactions_reference_key` ON `point_transactions` (`reference`);
-- Create index "pointtransaction_user_id_created_at" to table: "point_transactions"
CREATE INDEX `pointtransaction_user_id_created_at` ON `point_transactions` (`user_id`, `created_at`);
-- Create "posts" table
CREATE TABLE `posts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `slug` text NOT NULL, `type` text NOT NULL DEFAULT ('blog'), `title_json` json NOT NULL, `summary_json` json NULL, `content_json` text NOT NULL, `thumbnail` text NULL, `category_id` integer NULL, `is_published` bool NOT NULL DEFAULT (false), `published_at` datetime NULL);
-- Create index "post_subsite_id_slug" to table: "posts"
CREATE UNIQUE INDEX `post_subsite_id_slug` ON `posts` (`subsite_id`, `slug`);
-- Create index "post_type_is_published" to table: "posts"
CREATE INDEX `post_type_is_published` ON `posts` (`type`, `is_published`);
-- Create "post_categories" table
CREATE TABLE `post_categories` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `slug` text NOT NULL, `sort` integer NOT NULL DEFAULT (0));
-- Create index "post_categories_slug_key" to table: "post_categories"
CREATE UNIQUE INDEX `post_categories_slug_key` ON `post_categories` (`slug`);
-- Create "procurement_items" table
CREATE TABLE `procurement_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `procurement_id` integer NOT NULL, `upstream_sku` text NOT NULL DEFAULT (''), `quantity` integer NOT NULL, `unit_cost` integer NOT NULL DEFAULT (0), `received_content` blob NULL);
-- Create index "procurementitem_procurement_id" to table: "procurement_items"
CREATE INDEX `procurementitem_procurement_id` ON `procurement_items` (`procurement_id`);
-- Create "procurement_orders" table
CREATE TABLE `procurement_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `order_item_id` integer NOT NULL, `connection_id` integer NOT NULL, `upstream_order_id` text NULL, `status` text NOT NULL DEFAULT ('pending'), `fail_strategy` text NOT NULL DEFAULT ('auto_refund'), `retry_count` integer NOT NULL DEFAULT (0), `next_retry_at` datetime NULL, `last_poll_at` datetime NULL, `dedupe_key` text NOT NULL, `trace_id` text NULL);
-- Create index "procurement_orders_dedupe_key_key" to table: "procurement_orders"
CREATE UNIQUE INDEX `procurement_orders_dedupe_key_key` ON `procurement_orders` (`dedupe_key`);
-- Create index "procurementorder_connection_id_upstream_order_id" to table: "procurement_orders"
CREATE INDEX `procurementorder_connection_id_upstream_order_id` ON `procurement_orders` (`connection_id`, `upstream_order_id`);
-- Create index "procurementorder_order_item_id" to table: "procurement_orders"
CREATE INDEX `procurementorder_order_item_id` ON `procurement_orders` (`order_item_id`);
-- Create index "procurementorder_status_last_poll_at" to table: "procurement_orders"
CREATE INDEX `procurementorder_status_last_poll_at` ON `procurement_orders` (`status`, `last_poll_at`);
-- Create "product_controls" table
CREATE TABLE `product_controls` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `name` text NOT NULL, `type` text NOT NULL, `required` bool NOT NULL DEFAULT (false), `options` json NULL, `sort` integer NOT NULL DEFAULT (0));
-- Create index "productcontrol_product_id" to table: "product_controls"
CREATE INDEX `productcontrol_product_id` ON `product_controls` (`product_id`);
-- Create "promotions" table
CREATE TABLE `promotions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `scope` json NOT NULL, `type` text NOT NULL, `threshold` integer NOT NULL DEFAULT (0), `discount` integer NOT NULL DEFAULT (0), `special_price` integer NOT NULL DEFAULT (0), `start_at` datetime NOT NULL, `end_at` datetime NOT NULL, `enabled` bool NOT NULL DEFAULT (true));
-- Create "recharge_orders" table
CREATE TABLE `recharge_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `user_id` integer NOT NULL, `amount` integer NOT NULL, `gift_amount` integer NOT NULL DEFAULT (0), `gift_points` integer NOT NULL DEFAULT (0), `target` text NOT NULL DEFAULT ('balance'), `status` text NOT NULL DEFAULT ('pending'), `payment_id` integer NULL, `paid_at` datetime NULL);
-- Create index "rechargeorder_user_id_created_at" to table: "recharge_orders"
CREATE INDEX `rechargeorder_user_id_created_at` ON `recharge_orders` (`user_id`, `created_at`);
-- Create index "rechargeorder_status" to table: "recharge_orders"
CREATE INDEX `rechargeorder_status` ON `recharge_orders` (`status`);
-- Create "reconciliation_items" table
CREATE TABLE `reconciliation_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `job_id` integer NOT NULL, `procurement_order_id` integer NULL, `local_order_no` text NULL, `upstream_order_no` text NULL, `status` text NOT NULL, `diff_json` json NULL, `created_at` datetime NOT NULL);
-- Create index "reconciliationitem_job_id_status" to table: "reconciliation_items"
CREATE INDEX `reconciliationitem_job_id_status` ON `reconciliation_items` (`job_id`, `status`);
-- Create "reconciliation_jobs" table
CREATE TABLE `reconciliation_jobs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `connection_id` integer NOT NULL, `type` text NOT NULL DEFAULT ('orders'), `status` text NOT NULL DEFAULT ('pending'), `time_range_start` datetime NOT NULL, `time_range_end` datetime NOT NULL, `total_count` integer NOT NULL DEFAULT (0), `matched_count` integer NOT NULL DEFAULT (0), `mismatched_count` integer NOT NULL DEFAULT (0), `result_json` json NULL);
-- Create "reseller_balance_accounts" table
CREATE TABLE `reseller_balance_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `subsite_id` integer NOT NULL, `currency` text NOT NULL DEFAULT ('CNY'), `available` integer NOT NULL DEFAULT (0), `locked` integer NOT NULL DEFAULT (0), `negative` integer NOT NULL DEFAULT (0), `last_entry_id` integer NOT NULL DEFAULT (0), `updated_at` datetime NOT NULL);
-- Create index "reseller_balance_accounts_subsite_id_key" to table: "reseller_balance_accounts"
CREATE UNIQUE INDEX `reseller_balance_accounts_subsite_id_key` ON `reseller_balance_accounts` (`subsite_id`);
-- Create "reseller_ledger_entries" table
CREATE TABLE `reseller_ledger_entries` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `subsite_id` integer NOT NULL, `order_id` integer NULL, `type` text NOT NULL, `amount` integer NOT NULL, `currency` text NOT NULL DEFAULT ('CNY'), `status` text NOT NULL DEFAULT ('pending'), `available_at` datetime NULL, `idempotency_key` text NOT NULL, `metadata_json` json NULL, `remark` text NULL, `created_at` datetime NOT NULL);
-- Create index "reseller_ledger_entries_idempotency_key_key" to table: "reseller_ledger_entries"
CREATE UNIQUE INDEX `reseller_ledger_entries_idempotency_key_key` ON `reseller_ledger_entries` (`idempotency_key`);
-- Create index "resellerledgerentry_subsite_id_status" to table: "reseller_ledger_entries"
CREATE INDEX `resellerledgerentry_subsite_id_status` ON `reseller_ledger_entries` (`subsite_id`, `status`);
-- Create index "resellerledgerentry_available_at" to table: "reseller_ledger_entries"
CREATE INDEX `resellerledgerentry_available_at` ON `reseller_ledger_entries` (`available_at`);
-- Create "reseller_pricings" table
CREATE TABLE `reseller_pricings` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL, `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `mode` text NOT NULL DEFAULT ('inherit'), `value` integer NOT NULL DEFAULT (0));
-- Create index "resellerpricing_subsite_id_product_id_sku_id" to table: "reseller_pricings"
CREATE UNIQUE INDEX `resellerpricing_subsite_id_product_id_sku_id` ON `reseller_pricings` (`subsite_id`, `product_id`, `sku_id`);
-- Create "reseller_profiles" table
CREATE TABLE `reseller_profiles` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `user_id` integer NOT NULL, `status` text NOT NULL DEFAULT ('applying'), `apply_reason` text NULL, `reject_reason` text NULL, `level` integer NOT NULL DEFAULT (1), `default_markup_percent` real NOT NULL DEFAULT (0), `max_markup_percent` real NOT NULL DEFAULT (100), `confirm_days` integer NOT NULL DEFAULT (7), `reviewed_by` integer NULL, `reviewed_at` datetime NULL);
-- Create index "reseller_profiles_user_id_key" to table: "reseller_profiles"
CREATE UNIQUE INDEX `reseller_profiles_user_id_key` ON `reseller_profiles` (`user_id`);
-- Create "reseller_related_accounts" table
CREATE TABLE `reseller_related_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `reseller_id` integer NOT NULL, `user_id` integer NOT NULL, `relation_type` text NOT NULL, `source` text NULL, `status` text NOT NULL DEFAULT ('active'));
-- Create index "resellerrelatedaccount_reseller_id_user_id_relation_type" to table: "reseller_related_accounts"
CREATE UNIQUE INDEX `resellerrelatedaccount_reseller_id_user_id_relation_type` ON `reseller_related_accounts` (`reseller_id`, `user_id`, `relation_type`);
-- Create "reseller_sites" table
CREATE TABLE `reseller_sites` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `profile_id` integer NOT NULL, `domain` text NOT NULL, `type` text NOT NULL DEFAULT ('custom'), `verification_token` text NOT NULL, `verification_status` text NOT NULL DEFAULT ('pending'), `is_primary` bool NOT NULL DEFAULT (false), `site_name` text NULL, `logo` text NULL, `favicon` text NULL, `announcement_json` json NULL, `support_json` json NULL, `status` text NOT NULL DEFAULT ('active'));
-- Create index "reseller_sites_domain_key" to table: "reseller_sites"
CREATE UNIQUE INDEX `reseller_sites_domain_key` ON `reseller_sites` (`domain`);
-- Create "reviews" table
CREATE TABLE `reviews` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `user_id` integer NOT NULL, `order_id` integer NOT NULL, `rating` integer NOT NULL, `content` text NOT NULL, `status` text NOT NULL DEFAULT ('pending'));
-- Create index "review_order_id" to table: "reviews"
CREATE UNIQUE INDEX `review_order_id` ON `reviews` (`order_id`);
-- Create index "review_product_id_status" to table: "reviews"
CREATE INDEX `review_product_id_status` ON `reviews` (`product_id`, `status`);
-- Create "risk_lock_keys" table
CREATE TABLE `risk_lock_keys` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `key_hash` text NOT NULL, `expires_at` datetime NOT NULL, `created_at` datetime NOT NULL);
-- Create index "risklockkey_key_hash" to table: "risk_lock_keys"
CREATE UNIQUE INDEX `risklockkey_key_hash` ON `risk_lock_keys` (`key_hash`);
-- Create index "risklockkey_expires_at" to table: "risk_lock_keys"
CREATE INDEX `risklockkey_expires_at` ON `risk_lock_keys` (`expires_at`);
-- Create "security_audit_logs" table
CREATE TABLE `security_audit_logs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `actor_type` text NOT NULL, `actor_id` integer NULL, `action` text NOT NULL, `ip` text NULL, `metadata` json NULL, `created_at` datetime NOT NULL);
-- Create index "securityauditlog_actor_type_actor_id_created_at" to table: "security_audit_logs"
CREATE INDEX `securityauditlog_actor_type_actor_id_created_at` ON `security_audit_logs` (`actor_type`, `actor_id`, `created_at`);
-- Create index "securityauditlog_action_created_at" to table: "security_audit_logs"
CREATE INDEX `securityauditlog_action_created_at` ON `security_audit_logs` (`action`, `created_at`);
-- Create "sessions" table
CREATE TABLE `sessions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `realm` text NOT NULL, `user_id` integer NOT NULL, `refresh_token_hash` text NOT NULL, `device` text NULL, `ip` text NULL, `user_agent` text NULL, `expires_at` datetime NOT NULL, `revoked_at` datetime NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL);
-- Create index "session_refresh_token_hash" to table: "sessions"
CREATE UNIQUE INDEX `session_refresh_token_hash` ON `sessions` (`refresh_token_hash`);
-- Create index "session_realm_user_id" to table: "sessions"
CREATE INDEX `session_realm_user_id` ON `sessions` (`realm`, `user_id`);
-- Create index "session_expires_at" to table: "sessions"
CREATE INDEX `session_expires_at` ON `sessions` (`expires_at`);
-- Create "supplier_accounts" table
CREATE TABLE `supplier_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `api_key` text NOT NULL, `api_secret` blob NOT NULL, `contact` text NULL, `status` text NOT NULL DEFAULT ('applying'), `balance_cache` integer NOT NULL DEFAULT (0), `notify_url` text NULL, `reviewed_at` datetime NULL);
-- Create index "supplier_accounts_api_key_key" to table: "supplier_accounts"
CREATE UNIQUE INDEX `supplier_accounts_api_key_key` ON `supplier_accounts` (`api_key`);
-- Create "supplier_ledger_entries" table
CREATE TABLE `supplier_ledger_entries` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `account_id` integer NOT NULL, `supply_order_id` integer NULL, `type` text NOT NULL, `amount` integer NOT NULL, `currency` text NOT NULL DEFAULT ('CNY'), `reference` text NOT NULL, `remark` text NULL, `created_at` datetime NOT NULL);
-- Create index "supplier_ledger_entries_reference_key" to table: "supplier_ledger_entries"
CREATE UNIQUE INDEX `supplier_ledger_entries_reference_key` ON `supplier_ledger_entries` (`reference`);
-- Create index "supplierledgerentry_account_id_created_at" to table: "supplier_ledger_entries"
CREATE INDEX `supplierledgerentry_account_id_created_at` ON `supplier_ledger_entries` (`account_id`, `created_at`);
-- Create "supplier_product_prices" table
CREATE TABLE `supplier_product_prices` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `supplier_account_id` integer NOT NULL, `product_id` integer NOT NULL, `sku_id` integer NOT NULL DEFAULT (0), `price` integer NOT NULL);
-- Create index "supplierproductprice_supplier_account_id_product_id_sku_id" to table: "supplier_product_prices"
CREATE UNIQUE INDEX `supplierproductprice_supplier_account_id_product_id_sku_id` ON `supplier_product_prices` (`supplier_account_id`, `product_id`, `sku_id`);
-- Create "supply_connections" table
CREATE TABLE `supply_connections` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `driver` text NOT NULL, `base_url` text NOT NULL, `credentials` blob NOT NULL, `status` text NOT NULL DEFAULT ('active'), `callback_url` text NULL, `retry_max` integer NOT NULL DEFAULT (5), `retry_intervals` text NOT NULL DEFAULT ('[30,60,300]'), `exchange_rate` real NOT NULL DEFAULT (1), `price_markup_percent` real NOT NULL DEFAULT (0), `price_rounding_mode` text NOT NULL DEFAULT ('none'), `auto_sync_price` bool NOT NULL DEFAULT (true), `stock_mode` text NOT NULL DEFAULT ('real'), `settings` json NULL, `last_ping_at` datetime NULL, `last_ping_ok` bool NOT NULL DEFAULT (false), `last_synced_at` datetime NULL, `last_error` text NULL, `balance_cache` integer NOT NULL DEFAULT (0));
-- Create index "supplyconnection_status_driver" to table: "supply_connections"
CREATE INDEX `supplyconnection_status_driver` ON `supply_connections` (`status`, `driver`);
-- Create index "supplyconnection_last_synced_at" to table: "supply_connections"
CREATE INDEX `supplyconnection_last_synced_at` ON `supply_connections` (`last_synced_at`);
-- Create "supply_mappings" table
CREATE TABLE `supply_mappings` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `connection_id` integer NOT NULL, `upstream_category` text NULL, `local_category_id` integer NULL, `upstream_product` text NOT NULL, `local_product_id` integer NULL, `upstream_sku` text NOT NULL DEFAULT (''), `local_sku_id` integer NULL, `up_stock` integer NOT NULL DEFAULT (0), `pricing_override` json NULL);
-- Create index "supplymapping_connection_id_upstream_product_upstream_sku" to table: "supply_mappings"
CREATE UNIQUE INDEX `supplymapping_connection_id_upstream_product_upstream_sku` ON `supply_mappings` (`connection_id`, `upstream_product`, `upstream_sku`);
-- Create index "supplymapping_local_product_id" to table: "supply_mappings"
CREATE INDEX `supplymapping_local_product_id` ON `supply_mappings` (`local_product_id`);
-- Create "supply_nonces" table
CREATE TABLE `supply_nonces` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `key` text NOT NULL, `nonce` text NOT NULL, `expires_at` datetime NOT NULL, `created_at` datetime NOT NULL);
-- Create index "supplynonce_key_nonce" to table: "supply_nonces"
CREATE UNIQUE INDEX `supplynonce_key_nonce` ON `supply_nonces` (`key`, `nonce`);
-- Create index "supplynonce_expires_at" to table: "supply_nonces"
CREATE INDEX `supplynonce_expires_at` ON `supply_nonces` (`expires_at`);
-- Create "supply_orders" table
CREATE TABLE `supply_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `account_id` integer NOT NULL, `downstream_order_no` text NOT NULL, `items` json NOT NULL, `amount` integer NOT NULL, `status` text NOT NULL DEFAULT ('pending'), `local_order_id` integer NULL, `paid_at` datetime NULL, `fulfilled_at` datetime NULL);
-- Create index "supply_orders_downstream_order_no_key" to table: "supply_orders"
CREATE UNIQUE INDEX `supply_orders_downstream_order_no_key` ON `supply_orders` (`downstream_order_no`);
-- Create index "supplyorder_account_id_created_at" to table: "supply_orders"
CREATE INDEX `supplyorder_account_id_created_at` ON `supply_orders` (`account_id`, `created_at`);
-- Create index "supplyorder_status" to table: "supply_orders"
CREATE INDEX `supplyorder_status` ON `supply_orders` (`status`);
-- Create "supply_sync_tasks" table
CREATE TABLE `supply_sync_tasks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `connection_id` integer NOT NULL, `mode` text NOT NULL, `scope` text NULL, `force_reprice` bool NOT NULL DEFAULT (false), `status` text NOT NULL DEFAULT ('pending'), `total_count` integer NOT NULL DEFAULT (0), `processed_count` integer NOT NULL DEFAULT (0), `created_count` integer NOT NULL DEFAULT (0), `updated_count` integer NOT NULL DEFAULT (0), `price_updated_count` integer NOT NULL DEFAULT (0), `manual_skipped_count` integer NOT NULL DEFAULT (0), `hidden_count` integer NOT NULL DEFAULT (0), `deleted_count` integer NOT NULL DEFAULT (0), `error_code` text NULL, `error_context` text NULL, `started_at` datetime NULL, `heartbeat_at` datetime NULL, `current_stage` text NULL, `current_page` integer NOT NULL DEFAULT (0), `cancel_requested_at` datetime NULL, `worker_version` text NULL, `finished_at` datetime NULL);
-- Create index "supplysynctask_connection_id_created_at" to table: "supply_sync_tasks"
CREATE INDEX `supplysynctask_connection_id_created_at` ON `supply_sync_tasks` (`connection_id`, `created_at`);
-- Create index "supplysynctask_status" to table: "supply_sync_tasks"
CREATE INDEX `supplysynctask_status` ON `supply_sync_tasks` (`status`);
-- Create "tags" table
CREATE TABLE `tags` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `slug` text NOT NULL, `icon` text NULL, `color` text NULL, `position` text NOT NULL DEFAULT ('top_right'), `hide` bool NOT NULL DEFAULT (false));
-- Create index "tags_slug_key" to table: "tags"
CREATE UNIQUE INDEX `tags_slug_key` ON `tags` (`slug`);
-- Create "tickets" table
CREATE TABLE `tickets` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `ticket_no` text NOT NULL, `user_id` integer NULL, `guest_contact` text NULL, `type` text NOT NULL, `priority` text NOT NULL DEFAULT ('normal'), `order_id` integer NULL, `product_id` integer NULL, `status` text NOT NULL DEFAULT ('open'), `first_reply_at` datetime NULL, `sla_due_at` datetime NULL, `satisfaction` integer NULL);
-- Create index "tickets_ticket_no_key" to table: "tickets"
CREATE UNIQUE INDEX `tickets_ticket_no_key` ON `tickets` (`ticket_no`);
-- Create index "ticket_status_priority_created_at" to table: "tickets"
CREATE INDEX `ticket_status_priority_created_at` ON `tickets` (`status`, `priority`, `created_at`);
-- Create index "ticket_user_id_created_at" to table: "tickets"
CREATE INDEX `ticket_user_id_created_at` ON `tickets` (`user_id`, `created_at`);
-- Create "ticket_messages" table
CREATE TABLE `ticket_messages` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `ticket_id` integer NOT NULL, `sender_type` text NOT NULL, `sender_id` integer NULL, `content` text NOT NULL, `attachments` json NULL, `is_internal` bool NOT NULL DEFAULT (false), `created_at` datetime NOT NULL);
-- Create index "ticketmessage_ticket_id_created_at" to table: "ticket_messages"
CREATE INDEX `ticketmessage_ticket_id_created_at` ON `ticket_messages` (`ticket_id`, `created_at`);
-- Create "user_groups" table
CREATE TABLE `user_groups` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `code` text NOT NULL, `name` text NOT NULL, `level_id` integer NULL);
-- Create index "user_groups_code_key" to table: "user_groups"
CREATE UNIQUE INDEX `user_groups_code_key` ON `user_groups` (`code`);
-- Create "v1id_maps" table
CREATE TABLE `v1id_maps` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `table_name` text NOT NULL, `old_id` integer NOT NULL, `new_id` integer NOT NULL, `created_at` datetime NOT NULL);
-- Create index "v1idmap_table_name_old_id" to table: "v1id_maps"
CREATE UNIQUE INDEX `v1idmap_table_name_old_id` ON `v1id_maps` (`table_name`, `old_id`);
-- Create "virtual_reviews" table
CREATE TABLE `virtual_reviews` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `product_id` integer NOT NULL, `nickname` text NOT NULL, `content` text NOT NULL, `rating` integer NOT NULL DEFAULT (5), `sort` integer NOT NULL DEFAULT (0), `created_at` datetime NOT NULL);
-- Create index "virtualreview_product_id" to table: "virtual_reviews"
CREATE INDEX `virtualreview_product_id` ON `virtual_reviews` (`product_id`);
-- Create "visit_logs" table
CREATE TABLE `visit_logs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `stat_date` text NOT NULL, `stat_hour` integer NOT NULL, `path` text NOT NULL, `pv` integer NOT NULL DEFAULT (0), `uv` integer NOT NULL DEFAULT (0));
-- Create index "visitlog_subsite_id_stat_date_stat_hour_path" to table: "visit_logs"
CREATE UNIQUE INDEX `visitlog_subsite_id_stat_date_stat_hour_path` ON `visit_logs` (`subsite_id`, `stat_date`, `stat_hour`, `path`);
-- Create "withdrawals" table
CREATE TABLE `withdrawals` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `user_id` integer NOT NULL, `amount` integer NOT NULL, `fee` integer NOT NULL DEFAULT (0), `method` json NOT NULL, `status` text NOT NULL DEFAULT ('pending'), `reviewed_by` integer NULL, `reject_reason` text NULL, `paid_at` datetime NULL, `reviewed_at` datetime NULL);
-- Create index "withdrawal_user_id_status" to table: "withdrawals"
CREATE INDEX `withdrawal_user_id_status` ON `withdrawals` (`user_id`, `status`);
-- Create index "withdrawal_status_created_at" to table: "withdrawals"
CREATE INDEX `withdrawal_status_created_at` ON `withdrawals` (`status`, `created_at`);
