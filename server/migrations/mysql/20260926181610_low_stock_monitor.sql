-- Modify "supply_connections" table
ALTER TABLE `supply_connections` ADD COLUMN `low_stock_scanned_at` bigint NOT NULL DEFAULT 0, ADD COLUMN `low_stock_message` varchar(255) NOT NULL DEFAULT "";
-- Modify "supply_mappings" table
ALTER TABLE `supply_mappings` ADD COLUMN `stock_probe_after` bigint NOT NULL DEFAULT 0, ADD COLUMN `stock_probe_lease` bigint NOT NULL DEFAULT 0, ADD COLUMN `stock_probe_failures` bigint NOT NULL DEFAULT 0, ADD INDEX `supplymapping_connection_id_stock_probe_after_id` (`connection_id`, `stock_probe_after`, `id`);
-- Create "stock_alerts" table
CREATE TABLE `stock_alerts` (`id` bigint unsigned NOT NULL AUTO_INCREMENT, `created_at` datetime(3) NOT NULL, `updated_at` datetime(3) NOT NULL, `product_id` bigint unsigned NOT NULL, `sku_id` bigint unsigned NOT NULL DEFAULT 0, `source_key` varchar(255) NOT NULL, `threshold` bigint NOT NULL DEFAULT 0, `state` tinyint NOT NULL DEFAULT 0, `notified_state` tinyint NOT NULL DEFAULT 0, `last_notified_at` bigint NOT NULL DEFAULT 0, PRIMARY KEY (`id`), UNIQUE INDEX `stockalert_product_id_sku_id` (`product_id`, `sku_id`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
