-- Modify "users" table
ALTER TABLE `users` ADD COLUMN `referral_level_id` bigint unsigned NOT NULL DEFAULT 0, ADD COLUMN `invite_level_id` bigint unsigned NOT NULL DEFAULT 0, ADD INDEX `user_invite_level_id` (`invite_level_id`), ADD INDEX `user_referral_level_id` (`referral_level_id`);
