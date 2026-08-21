-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_users" table
CREATE TABLE `new_users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `username` text NOT NULL, `email` text NULL, `password_hash` text NULL, `status` text NOT NULL DEFAULT ('active'), `last_login_at` datetime NULL, `invite_l1` integer NULL, `invite_l2` integer NULL, `invite_l3` integer NULL, `promo_code` text NULL);
-- Copy rows from old table "users" to new temporary table "new_users"
INSERT INTO `new_users` (`id`, `created_at`, `updated_at`, `username`, `email`, `password_hash`, `status`, `last_login_at`, `invite_l1`, `invite_l2`, `invite_l3`) SELECT `id`, `created_at`, `updated_at`, `username`, `email`, `password_hash`, `status`, `last_login_at`, `invite_l1`, `invite_l2`, `invite_l3` FROM `users`;
-- Drop "users" table after copying rows
DROP TABLE `users`;
-- Rename temporary table "new_users" to "users"
ALTER TABLE `new_users` RENAME TO `users`;
-- Create index "users_username_key" to table: "users"
CREATE UNIQUE INDEX `users_username_key` ON `users` (`username`);
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX `users_email_key` ON `users` (`email`);
-- Create index "users_promo_code_key" to table: "users"
CREATE UNIQUE INDEX `users_promo_code_key` ON `users` (`promo_code`);
-- Create index "user_invite_l1" to table: "users"
CREATE INDEX `user_invite_l1` ON `users` (`invite_l1`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
