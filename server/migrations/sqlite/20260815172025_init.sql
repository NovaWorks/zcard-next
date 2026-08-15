-- Create "admin_roles" table
CREATE TABLE `admin_roles` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `code` text NOT NULL, `description` text NULL, `is_builtin` bool NOT NULL DEFAULT (false));
-- Create index "admin_roles_name_key" to table: "admin_roles"
CREATE UNIQUE INDEX `admin_roles_name_key` ON `admin_roles` (`name`);
-- Create index "admin_roles_code_key" to table: "admin_roles"
CREATE UNIQUE INDEX `admin_roles_code_key` ON `admin_roles` (`code`);
-- Create "admin_users" table
CREATE TABLE `admin_users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `username` text NOT NULL, `password_hash` text NOT NULL, `nickname` text NULL, `avatar` text NULL, `role_id` integer NOT NULL, `totp_secret` blob NULL, `enabled` bool NOT NULL DEFAULT (true), `remark` text NULL, `last_login_ip` text NULL, `last_login_at` datetime NULL);
-- Create index "admin_users_username_key" to table: "admin_users"
CREATE UNIQUE INDEX `admin_users_username_key` ON `admin_users` (`username`);
-- Create index "adminuser_role_id" to table: "admin_users"
CREATE INDEX `adminuser_role_id` ON `admin_users` (`role_id`);
-- Create "cards" table
CREATE TABLE `cards` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `version` integer NOT NULL DEFAULT (0), `content` blob NOT NULL, `content_hash` text NOT NULL, `number_hash` text NULL, `status` text NOT NULL DEFAULT ('available'), `order_id` integer NULL, `sku_id` integer NULL, `import_id` integer NULL, `card_type` text NULL, `note` text NULL, `owner_id` integer NOT NULL DEFAULT (0), `draft_premium` integer NOT NULL DEFAULT (0), `draft_cost` integer NOT NULL DEFAULT (0), `price` integer NULL, `locked_at` datetime NULL, `used_at` datetime NULL, `product_id` integer NOT NULL, CONSTRAINT `cards_products_cards` FOREIGN KEY (`product_id`) REFERENCES `products` (`id`) ON DELETE NO ACTION);
-- Create index "card_subsite_id_product_id_content_hash" to table: "cards"
CREATE UNIQUE INDEX `card_subsite_id_product_id_content_hash` ON `cards` (`subsite_id`, `product_id`, `content_hash`);
-- Create index "card_product_id_status" to table: "cards"
CREATE INDEX `card_product_id_status` ON `cards` (`product_id`, `status`);
-- Create index "card_subsite_id_status" to table: "cards"
CREATE INDEX `card_subsite_id_status` ON `cards` (`subsite_id`, `status`);
-- Create index "card_order_id" to table: "cards"
CREATE INDEX `card_order_id` ON `cards` (`order_id`);
-- Create index "card_import_id" to table: "cards"
CREATE INDEX `card_import_id` ON `cards` (`import_id`);
-- Create "currencies" table
CREATE TABLE `currencies` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `code` text NOT NULL, `symbol` text NOT NULL, `position` text NOT NULL DEFAULT ('prefix'), `precision` integer NOT NULL DEFAULT (2), `rate` real NOT NULL DEFAULT (1), `enabled` bool NOT NULL DEFAULT (true), `sort` integer NOT NULL DEFAULT (0));
-- Create index "currencies_code_key" to table: "currencies"
CREATE UNIQUE INDEX `currencies_code_key` ON `currencies` (`code`);
-- Create index "currency_enabled" to table: "currencies"
CREATE INDEX `currency_enabled` ON `currencies` (`enabled`);
-- Create "orders" table
CREATE TABLE `orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `version` integer NOT NULL DEFAULT (0), `order_no` text NOT NULL, `subsite_domain` text NULL, `subsite_profit` integer NOT NULL DEFAULT (0), `profit_eligible` bool NOT NULL DEFAULT (true), `user_id` integer NULL, `guest_contact` text NULL, `query_password_hash` text NULL, `status` text NOT NULL DEFAULT ('pending_payment'), `total_amount` integer NOT NULL DEFAULT (0), `cost` integer NOT NULL DEFAULT (0), `base_currency` text NULL, `display_currency` text NULL, `exchange_rate` real NULL, `amount_display` integer NULL, `payment_channel` text NULL, `contact` text NULL, `client_ip` text NULL, `risk_ip` text NULL, `risk_flags` json NULL, `parent_id` integer NULL, `escrow_id` integer NULL, `invite_l1` integer NULL, `invite_l2` integer NULL, `invite_l3` integer NULL, `extra` json NULL, `paid_at` datetime NULL, `closed_at` datetime NULL, `expired_at` datetime NULL);
-- Create index "orders_order_no_key" to table: "orders"
CREATE UNIQUE INDEX `orders_order_no_key` ON `orders` (`order_no`);
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
-- Create "order_amount_lines" table
CREATE TABLE `order_amount_lines` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `item_id` integer NULL, `type` text NOT NULL, `amount` integer NOT NULL, `source_type` text NULL, `source_id` integer NULL, `seq` integer NOT NULL DEFAULT (0), `meta` json NULL, `created_at` datetime NOT NULL, `order_id` integer NOT NULL, CONSTRAINT `order_amount_lines_orders_amount_lines` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Create index "orderamountline_order_id" to table: "order_amount_lines"
CREATE INDEX `orderamountline_order_id` ON `order_amount_lines` (`order_id`);
-- Create index "orderamountline_item_id" to table: "order_amount_lines"
CREATE INDEX `orderamountline_item_id` ON `order_amount_lines` (`item_id`);
-- Create "order_deliveries" table
CREATE TABLE `order_deliveries` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `item_id` integer NOT NULL, `card_id` integer NOT NULL, `delivery_token_hash` text NOT NULL, `delivered_mode` text NOT NULL, `delivered_by` integer NOT NULL DEFAULT (0), `logistics` json NULL, `fetch_count` integer NOT NULL DEFAULT (0), `delivered_at` datetime NULL, `fetched_ip` text NULL, `order_id` integer NOT NULL, CONSTRAINT `order_deliveries_orders_deliveries` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Create index "orderdelivery_order_id" to table: "order_deliveries"
CREATE INDEX `orderdelivery_order_id` ON `order_deliveries` (`order_id`);
-- Create index "orderdelivery_card_id" to table: "order_deliveries"
CREATE INDEX `orderdelivery_card_id` ON `order_deliveries` (`card_id`);
-- Create "order_items" table
CREATE TABLE `order_items` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `product_id` integer NOT NULL, `sku_id` integer NULL, `sku_name` text NULL, `unit_price` integer NOT NULL, `quantity` integer NOT NULL, `amount` integer NOT NULL, `cost` integer NOT NULL DEFAULT (0), `fulfillment_type` text NOT NULL, `fulfillment_status` text NOT NULL DEFAULT ('pending'), `commission_snapshot` json NULL, `profit_snapshot` json NULL, `order_id` integer NOT NULL, CONSTRAINT `order_items_orders_items` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Create index "orderitem_order_id" to table: "order_items"
CREATE INDEX `orderitem_order_id` ON `order_items` (`order_id`);
-- Create index "orderitem_product_id" to table: "order_items"
CREATE INDEX `orderitem_product_id` ON `order_items` (`product_id`);
-- Create "order_status_events" table
CREATE TABLE `order_status_events` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `from_status` text NOT NULL, `to_status` text NOT NULL, `event` text NOT NULL, `operator` text NOT NULL, `operator_id` integer NULL, `reason` text NULL, `client_ip` text NULL, `created_at` datetime NOT NULL, `order_id` integer NOT NULL, CONSTRAINT `order_status_events_orders_status_events` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Create index "orderstatusevent_order_id_created_at" to table: "order_status_events"
CREATE INDEX `orderstatusevent_order_id_created_at` ON `order_status_events` (`order_id`, `created_at`);
-- Create index "orderstatusevent_event" to table: "order_status_events"
CREATE INDEX `orderstatusevent_event` ON `order_status_events` (`event`);
-- Create "outbox_events" table
CREATE TABLE `outbox_events` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `module` text NOT NULL, `type` text NOT NULL, `aggregate_id` text NOT NULL, `payload` json NULL, `dedupe_key` text NOT NULL, `status` text NOT NULL DEFAULT ('publishing'), `published_at` datetime NULL, `created_at` datetime NOT NULL);
-- Create index "outbox_events_dedupe_key_key" to table: "outbox_events"
CREATE UNIQUE INDEX `outbox_events_dedupe_key_key` ON `outbox_events` (`dedupe_key`);
-- Create index "outboxevent_status_created_at" to table: "outbox_events"
CREATE INDEX `outboxevent_status_created_at` ON `outbox_events` (`status`, `created_at`);
-- Create "payments" table
CREATE TABLE `payments` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `channel` text NOT NULL, `channel_order_no` text NULL, `amount` integer NOT NULL, `charged_amount` integer NOT NULL DEFAULT (0), `fee` integer NOT NULL DEFAULT (0), `status` text NOT NULL DEFAULT ('pending'), `paid_at` datetime NULL, `raw` json NULL, `idempotency_key` text NULL, `order_id` integer NOT NULL, CONSTRAINT `payments_orders_payments` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Create index "payment_order_id" to table: "payments"
CREATE INDEX `payment_order_id` ON `payments` (`order_id`);
-- Create index "payment_channel_channel_order_no" to table: "payments"
CREATE UNIQUE INDEX `payment_channel_channel_order_no` ON `payments` (`channel`, `channel_order_no`);
-- Create index "payment_status_created_at" to table: "payments"
CREATE INDEX `payment_status_created_at` ON `payments` (`status`, `created_at`);
-- Create "payment_channels" table
CREATE TABLE `payment_channels` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `code` text NOT NULL, `driver` text NOT NULL, `config` blob NOT NULL, `fee` integer NOT NULL DEFAULT (0), `fee_type` text NOT NULL DEFAULT ('fixed'), `fee_bearer` text NOT NULL DEFAULT ('merchant'), `sort` integer NOT NULL DEFAULT (0), `enabled` bool NOT NULL DEFAULT (true));
-- Create index "paymentchannel_subsite_id_code" to table: "payment_channels"
CREATE UNIQUE INDEX `paymentchannel_subsite_id_code` ON `payment_channels` (`subsite_id`, `code`);
-- Create "processed_events" table
CREATE TABLE `processed_events` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `event_id` integer NOT NULL, `consumer` text NOT NULL, `processed_at` datetime NOT NULL);
-- Create index "processedevent_event_id_consumer" to table: "processed_events"
CREATE UNIQUE INDEX `processedevent_event_id_consumer` ON `processed_events` (`event_id`, `consumer`);
-- Create "products" table
CREATE TABLE `products` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `category_id` integer NULL, `name` text NOT NULL, `slug` text NOT NULL, `description` text NULL, `cover` text NULL, `images` json NULL, `price` integer NOT NULL DEFAULT (0), `factory_price` integer NOT NULL DEFAULT (0), `draft_premium` integer NOT NULL DEFAULT (0), `member_price` json NULL, `stock_type` text NOT NULL DEFAULT ('card'), `stock_visible` bool NOT NULL DEFAULT (true), `delivery_mode` text NOT NULL DEFAULT ('status'), `control_config` json NULL, `dedup` bool NOT NULL DEFAULT (true), `sort` integer NOT NULL DEFAULT (0), `status` integer NOT NULL DEFAULT (1), `upstream_source_id` integer NULL, `upstream_product_code` text NULL, `upstream_synced_at` datetime NULL);
-- Create index "product_subsite_id_slug" to table: "products"
CREATE UNIQUE INDEX `product_subsite_id_slug` ON `products` (`subsite_id`, `slug`);
-- Create index "product_subsite_id_category_id" to table: "products"
CREATE INDEX `product_subsite_id_category_id` ON `products` (`subsite_id`, `category_id`);
-- Create index "product_subsite_id_status" to table: "products"
CREATE INDEX `product_subsite_id_status` ON `products` (`subsite_id`, `status`);
-- Create index "product_upstream_source_id" to table: "products"
CREATE INDEX `product_upstream_source_id` ON `products` (`upstream_source_id`);
-- Create "product_skus" table
CREATE TABLE `product_skus` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `name` text NOT NULL, `spec_values` json NOT NULL, `price` integer NULL, `cost` integer NULL, `stock_offset` integer NOT NULL DEFAULT (0), `upstream_sku_id` text NULL, `product_id` integer NOT NULL, CONSTRAINT `product_skus_products_skus` FOREIGN KEY (`product_id`) REFERENCES `products` (`id`) ON DELETE NO ACTION);
-- Create index "productsku_product_id" to table: "product_skus"
CREATE INDEX `productsku_product_id` ON `product_skus` (`product_id`);
-- Create index "productsku_product_id_name" to table: "product_skus"
CREATE UNIQUE INDEX `productsku_product_id_name` ON `product_skus` (`product_id`, `name`);
-- Create "refund_orders" table
CREATE TABLE `refund_orders` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `amount` integer NOT NULL, `channel` text NOT NULL, `status` text NOT NULL DEFAULT ('created'), `reason` text NULL, `operator_id` integer NULL, `upstream_refund_id` text NULL, `order_id` integer NOT NULL, CONSTRAINT `refund_orders_orders_refunds` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE NO ACTION);
-- Create index "refundorder_order_id" to table: "refund_orders"
CREATE INDEX `refundorder_order_id` ON `refund_orders` (`order_id`);
-- Create index "refundorder_status" to table: "refund_orders"
CREATE INDEX `refundorder_status` ON `refund_orders` (`status`);
-- Create "role_permissions" table
CREATE TABLE `role_permissions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `role_id` integer NOT NULL, `permission_code` text NOT NULL);
-- Create index "rolepermission_role_id_permission_code" to table: "role_permissions"
CREATE UNIQUE INDEX `rolepermission_role_id_permission_code` ON `role_permissions` (`role_id`, `permission_code`);
-- Create "settings" table
CREATE TABLE `settings` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `group` text NOT NULL, `key` text NOT NULL, `value` json NOT NULL);
-- Create index "setting_group_key" to table: "settings"
CREATE UNIQUE INDEX `setting_group_key` ON `settings` (`group`, `key`);
-- Create "users" table
CREATE TABLE `users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `username` text NOT NULL, `email` text NULL, `password_hash` text NULL, `status` text NOT NULL DEFAULT ('active'), `last_login_at` datetime NULL, `invite_l1` integer NULL, `invite_l2` integer NULL, `invite_l3` integer NULL);
-- Create index "users_username_key" to table: "users"
CREATE UNIQUE INDEX `users_username_key` ON `users` (`username`);
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX `users_email_key` ON `users` (`email`);
-- Create index "user_invite_l1" to table: "users"
CREATE INDEX `user_invite_l1` ON `users` (`invite_l1`);
-- Create "wallet_accounts" table
CREATE TABLE `wallet_accounts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `user_id` integer NOT NULL, `currency` text NOT NULL DEFAULT ('CNY'), `available` integer NOT NULL DEFAULT (0), `locked` integer NOT NULL DEFAULT (0), `version` integer NOT NULL DEFAULT (0), `updated_at` datetime NOT NULL);
-- Create index "wallet_accounts_user_id_key" to table: "wallet_accounts"
CREATE UNIQUE INDEX `wallet_accounts_user_id_key` ON `wallet_accounts` (`user_id`);
-- Create "wallet_transactions" table
CREATE TABLE `wallet_transactions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `user_id` integer NOT NULL, `direction` text NOT NULL, `type` text NOT NULL, `amount` integer NOT NULL, `balance_before` integer NOT NULL, `balance_after` integer NOT NULL, `currency` text NOT NULL DEFAULT ('CNY'), `reference` text NOT NULL, `order_id` integer NULL, `operator_id` integer NULL, `remark` text NULL, `created_at` datetime NOT NULL);
-- Create index "wallet_transactions_reference_key" to table: "wallet_transactions"
CREATE UNIQUE INDEX `wallet_transactions_reference_key` ON `wallet_transactions` (`reference`);
-- Create index "wallettransaction_user_id_created_at" to table: "wallet_transactions"
CREATE INDEX `wallettransaction_user_id_created_at` ON `wallet_transactions` (`user_id`, `created_at`);
-- Create index "wallettransaction_type_created_at" to table: "wallet_transactions"
CREATE INDEX `wallettransaction_type_created_at` ON `wallet_transactions` (`type`, `created_at`);
