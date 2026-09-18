-- Additive upgrade: keep mappings, credentials, prices and current stock intact.
ALTER TABLE `supply_mappings` ADD COLUMN `stock_reference` integer NOT NULL DEFAULT -2;
ALTER TABLE `supply_mappings` ADD COLUMN `stock_reference_at` datetime NULL;
UPDATE `supply_mappings` SET `stock_reference` = `up_stock`, `stock_reference_at` = `stock_checked_at` WHERE `up_stock` >= -1 AND `stock_checked_at` IS NOT NULL;
