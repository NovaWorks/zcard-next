-- Add column "ip_whitelist" to table: "supplier_accounts"
ALTER TABLE `supplier_accounts` ADD COLUMN `ip_whitelist` json NULL;
