-- Add column "failure_count" to table: "risk_lock_keys"
ALTER TABLE `risk_lock_keys` ADD COLUMN `failure_count` integer NOT NULL DEFAULT 5;
