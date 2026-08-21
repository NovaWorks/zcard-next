-- Modify "email_verifications" table
ALTER TABLE `email_verifications` MODIFY COLUMN `purpose` enum('register','phone_register','reset') NOT NULL;
-- Modify "recharge_orders" table
ALTER TABLE `recharge_orders` ADD COLUMN `supplier_account_id` bigint unsigned NULL;
-- Modify "supplier_accounts" table
ALTER TABLE `supplier_accounts` MODIFY COLUMN `status` enum('applying','approved','rejected','disabled') NOT NULL DEFAULT "applying", ADD COLUMN `owner_user_id` bigint unsigned NULL DEFAULT 0, ADD COLUMN `apply_reason` varchar(500) NULL, ADD COLUMN `review_note` varchar(500) NULL, ADD COLUMN `ip_whitelist` json NULL, ADD INDEX `supplieraccount_owner_user_id` (`owner_user_id`);
-- Modify "tickets" table
ALTER TABLE `tickets` MODIFY COLUMN `type` enum('presale','aftersale','withdraw') NOT NULL;
-- Modify "users" table
ALTER TABLE `users` ADD COLUMN `phone` varchar(20) NULL, ADD COLUMN `promo_code` varchar(16) NULL, ADD UNIQUE INDEX `phone` (`phone`), ADD UNIQUE INDEX `promo_code` (`promo_code`);
-- Modify "withdrawals" table
ALTER TABLE `withdrawals` ADD COLUMN `receipt` varchar(255) NULL;
-- Create "page_views" table
CREATE TABLE `page_views` (`id` bigint unsigned NOT NULL AUTO_INCREMENT, `created_at` datetime(3) NOT NULL, `updated_at` datetime(3) NOT NULL, `subsite_id` bigint unsigned NOT NULL DEFAULT 0, `day` varchar(8) NOT NULL, `path` varchar(255) NOT NULL, `user_id` bigint unsigned NULL, `ip` varchar(64) NOT NULL, PRIMARY KEY (`id`), INDEX `pageview_day_ip` (`day`, `ip`), INDEX `pageview_day_subsite_id` (`day`, `subsite_id`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
-- Create "user_sessions" table
CREATE TABLE `user_sessions` (`id` bigint unsigned NOT NULL AUTO_INCREMENT, `created_at` datetime(3) NOT NULL, `updated_at` datetime(3) NOT NULL, `subsite_id` bigint unsigned NOT NULL DEFAULT 0, `user_id` bigint unsigned NOT NULL, `ip` varchar(64) NOT NULL, `last_active_at` datetime(3) NOT NULL, PRIMARY KEY (`id`), INDEX `usersession_last_active_at` (`last_active_at`), UNIQUE INDEX `usersession_subsite_id_user_id` (`subsite_id`, `user_id`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
