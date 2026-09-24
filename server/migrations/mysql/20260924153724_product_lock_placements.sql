-- Modify "categories" table
ALTER TABLE `categories` ADD COLUMN `placement_version` bigint NOT NULL DEFAULT 0;
-- Modify "product_content_batches" table
ALTER TABLE `product_content_batches` ADD COLUMN `skipped_locked` int NOT NULL DEFAULT 0;
-- Modify "products" table
ALTER TABLE `products` ADD COLUMN `is_locked` bool NOT NULL DEFAULT 0, ADD COLUMN `lock_version` bigint NOT NULL DEFAULT 0, ADD COLUMN `locked_by` bigint unsigned NOT NULL DEFAULT 0, ADD COLUMN `locked_at` datetime(3) NULL;
-- Create "category_product_placements" table
CREATE TABLE `category_product_placements` (`id` bigint unsigned NOT NULL AUTO_INCREMENT, `created_at` datetime(3) NOT NULL, `updated_at` datetime(3) NOT NULL, `subsite_id` bigint unsigned NOT NULL DEFAULT 0, `category_id` bigint unsigned NOT NULL, `product_id` bigint unsigned NOT NULL, `is_pinned` bool NOT NULL DEFAULT 0, `is_recommended` bool NOT NULL DEFAULT 0, `position` int NOT NULL DEFAULT 0, PRIMARY KEY (`id`), UNIQUE INDEX `categoryproductplacement_subsite_id_category_id_product_id` (`subsite_id`, `category_id`, `product_id`), INDEX `categoryproductplacement_subsite_id_product_id` (`subsite_id`, `product_id`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
