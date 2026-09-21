-- Modify "products" table
ALTER TABLE `products` ADD COLUMN `cover_protected` bool NOT NULL DEFAULT 0, ADD COLUMN `description_protected` bool NOT NULL DEFAULT 0;
-- Create "product_content_batches" table
CREATE TABLE `product_content_batches` (`id` bigint unsigned NOT NULL AUTO_INCREMENT, `created_at` datetime(3) NOT NULL, `updated_at` datetime(3) NOT NULL, `subsite_id` bigint unsigned NOT NULL DEFAULT 0, `token` varchar(64) NOT NULL, `actor_id` bigint unsigned NOT NULL, `payload` json NOT NULL, `expires_at` datetime(3) NOT NULL, `completed` bool NOT NULL DEFAULT 0, `matched` int NOT NULL DEFAULT 0, `changed` int NOT NULL DEFAULT 0, PRIMARY KEY (`id`), UNIQUE INDEX `token` (`token`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
