-- Modify "card_imports" table
ALTER TABLE `card_imports` ADD COLUMN `sku_id` bigint unsigned NULL, ADD COLUMN `sku_name` varchar(100) NOT NULL DEFAULT "";
