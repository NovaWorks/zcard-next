-- Modify "risk_lock_keys" table
ALTER TABLE `risk_lock_keys` ADD COLUMN `failure_count` bigint NOT NULL DEFAULT 5;
