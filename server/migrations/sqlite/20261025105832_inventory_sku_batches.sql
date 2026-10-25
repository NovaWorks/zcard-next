-- Add column "sku_id" to table: "card_imports"
ALTER TABLE `card_imports` ADD COLUMN `sku_id` integer NULL;
-- Add column "sku_name" to table: "card_imports"
ALTER TABLE `card_imports` ADD COLUMN `sku_name` text NOT NULL DEFAULT '';
