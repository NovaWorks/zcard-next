-- Add column "last_collect_at" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `last_collect_at` datetime NULL;
-- Add column "last_price_sync_at" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `last_price_sync_at` datetime NULL;
-- Add column "last_status_sync_at" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `last_status_sync_at` datetime NULL;
-- Add column "rate_state" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `rate_state` json NULL;
-- Add column "rate_limit_until" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `rate_limit_until` datetime NULL;
