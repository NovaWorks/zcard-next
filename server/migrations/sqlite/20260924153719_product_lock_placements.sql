-- Add column "placement_version" to table: "categories"
ALTER TABLE `categories` ADD COLUMN `placement_version` integer NOT NULL DEFAULT 0;
-- Add column "skipped_locked" to table: "product_content_batches"
ALTER TABLE `product_content_batches` ADD COLUMN `skipped_locked` integer NOT NULL DEFAULT 0;
-- Add column "is_locked" to table: "products"
ALTER TABLE `products` ADD COLUMN `is_locked` bool NOT NULL DEFAULT false;
-- Add column "lock_version" to table: "products"
ALTER TABLE `products` ADD COLUMN `lock_version` integer NOT NULL DEFAULT 0;
-- Add column "locked_by" to table: "products"
ALTER TABLE `products` ADD COLUMN `locked_by` integer NOT NULL DEFAULT 0;
-- Add column "locked_at" to table: "products"
ALTER TABLE `products` ADD COLUMN `locked_at` datetime NULL;
-- Create "category_product_placements" table
CREATE TABLE `category_product_placements` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `category_id` integer NOT NULL, `product_id` integer NOT NULL, `is_pinned` bool NOT NULL DEFAULT (false), `is_recommended` bool NOT NULL DEFAULT (false), `position` integer NOT NULL DEFAULT (0));
-- Create index "categoryproductplacement_subsite_id_category_id_product_id" to table: "category_product_placements"
CREATE UNIQUE INDEX `categoryproductplacement_subsite_id_category_id_product_id` ON `category_product_placements` (`subsite_id`, `category_id`, `product_id`);
-- Create index "categoryproductplacement_subsite_id_product_id" to table: "category_product_placements"
CREATE INDEX `categoryproductplacement_subsite_id_product_id` ON `category_product_placements` (`subsite_id`, `product_id`);
