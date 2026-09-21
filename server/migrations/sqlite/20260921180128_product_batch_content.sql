-- Add column "cover_protected" to table: "products"
ALTER TABLE `products` ADD COLUMN `cover_protected` bool NOT NULL DEFAULT 0;
-- Add column "description_protected" to table: "products"
ALTER TABLE `products` ADD COLUMN `description_protected` bool NOT NULL DEFAULT 0;
-- Create "product_content_batches" table
CREATE TABLE `product_content_batches` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `token` text NOT NULL, `actor_id` integer NOT NULL, `payload` json NOT NULL, `expires_at` datetime NOT NULL, `completed` bool NOT NULL DEFAULT (false), `matched` integer NOT NULL DEFAULT (0), `changed` integer NOT NULL DEFAULT (0));
-- Create index "product_content_batches_token_key" to table: "product_content_batches"
CREATE UNIQUE INDEX `product_content_batches_token_key` ON `product_content_batches` (`token`);
