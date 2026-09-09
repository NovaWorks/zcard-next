-- Modify "orders" table
ALTER TABLE `orders` ADD COLUMN `expiry_retry_at` datetime(3) NULL, ADD COLUMN `expiry_attempts` int NOT NULL DEFAULT 0, ADD COLUMN `expiry_review` bool NOT NULL DEFAULT 0, ADD COLUMN `expiry_reason` varchar(255) NOT NULL DEFAULT "";
-- Modify "payments" table
ALTER TABLE `payments` ADD COLUMN `channel_id` bigint unsigned NOT NULL DEFAULT 0, ADD COLUMN `driver_snapshot` varchar(100) NOT NULL DEFAULT "", ADD COLUMN `expires_at` datetime(3) NULL, ADD COLUMN `review_reason` varchar(255) NOT NULL DEFAULT "";
-- Modify "supply_mappings" table
ALTER TABLE `supply_mappings` ADD COLUMN `stock_checked_at` datetime(3) NULL;
