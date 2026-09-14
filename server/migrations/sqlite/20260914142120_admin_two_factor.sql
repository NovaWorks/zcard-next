-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_admin_users" table
CREATE TABLE `new_admin_users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `username` text NOT NULL, `password_hash` text NOT NULL, `nickname` text NULL, `avatar` text NULL, `role_id` integer NOT NULL, `totp_secret` blob NULL, `auth_version` integer NOT NULL DEFAULT (0), `mfa_revision` integer NOT NULL DEFAULT (0), `mfa_state` text NOT NULL DEFAULT ('{}'), `enabled` bool NOT NULL DEFAULT (true), `remark` text NULL, `last_login_ip` text NULL, `last_login_at` datetime NULL);
-- Copy rows from old table "admin_users" to new temporary table "new_admin_users"
INSERT INTO `new_admin_users` (`id`, `created_at`, `updated_at`, `username`, `password_hash`, `nickname`, `avatar`, `role_id`, `totp_secret`, `enabled`, `remark`, `last_login_ip`, `last_login_at`) SELECT `id`, `created_at`, `updated_at`, `username`, `password_hash`, `nickname`, `avatar`, `role_id`, `totp_secret`, `enabled`, `remark`, `last_login_ip`, `last_login_at` FROM `admin_users`;
-- Drop "admin_users" table after copying rows
DROP TABLE `admin_users`;
-- Rename temporary table "new_admin_users" to "admin_users"
ALTER TABLE `new_admin_users` RENAME TO `admin_users`;
-- Create index "admin_users_username_key" to table: "admin_users"
CREATE UNIQUE INDEX `admin_users_username_key` ON `admin_users` (`username`);
-- Create index "adminuser_role_id" to table: "admin_users"
CREATE INDEX `adminuser_role_id` ON `admin_users` (`role_id`);
-- Create "new_sessions" table
CREATE TABLE `new_sessions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `realm` text NOT NULL, `user_id` integer NOT NULL, `refresh_token_hash` text NOT NULL, `auth_version` integer NOT NULL DEFAULT (0), `device` text NULL, `ip` text NULL, `user_agent` text NULL, `expires_at` datetime NOT NULL, `revoked_at` datetime NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL);
-- Copy rows from old table "sessions" to new temporary table "new_sessions"
INSERT INTO `new_sessions` (`id`, `realm`, `user_id`, `refresh_token_hash`, `device`, `ip`, `user_agent`, `expires_at`, `revoked_at`, `created_at`, `updated_at`) SELECT `id`, `realm`, `user_id`, `refresh_token_hash`, `device`, `ip`, `user_agent`, `expires_at`, `revoked_at`, `created_at`, `updated_at` FROM `sessions`;
-- Drop "sessions" table after copying rows
DROP TABLE `sessions`;
-- Rename temporary table "new_sessions" to "sessions"
ALTER TABLE `new_sessions` RENAME TO `sessions`;
-- Create index "session_refresh_token_hash" to table: "sessions"
CREATE UNIQUE INDEX `session_refresh_token_hash` ON `sessions` (`refresh_token_hash`);
-- Create index "session_realm_user_id" to table: "sessions"
CREATE INDEX `session_realm_user_id` ON `sessions` (`realm`, `user_id`);
-- Create index "session_expires_at" to table: "sessions"
CREATE INDEX `session_expires_at` ON `sessions` (`expires_at`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
