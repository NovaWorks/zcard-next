-- Add column "sms_lease_token" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `sms_lease_token` text NOT NULL DEFAULT '';
-- Add column "sms_lease_until" to table: "supply_connections"
ALTER TABLE `supply_connections` ADD COLUMN `sms_lease_until` integer NOT NULL DEFAULT 0;
