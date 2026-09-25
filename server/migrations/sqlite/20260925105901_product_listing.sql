-- Add column "auto_listing" to table: "products"
ALTER TABLE `products` ADD COLUMN `auto_listing` bool NOT NULL DEFAULT false;
-- Add column "listing_reason" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_reason` text NOT NULL DEFAULT '';
-- Add column "listing_restore_status" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_restore_status` integer NOT NULL DEFAULT 1;
-- Add column "listing_changed_at" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_changed_at` integer NOT NULL DEFAULT 0;
-- Add column "listing_observed_at" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_observed_at` integer NOT NULL DEFAULT 0;
-- Add column "listing_zero_since" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_zero_since` integer NOT NULL DEFAULT 0;
-- Add column "listing_last_stock" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_last_stock` integer NOT NULL DEFAULT -2;
-- Add column "listing_restocked" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_restocked` bool NOT NULL DEFAULT false;
-- Add column "listing_message" to table: "products"
ALTER TABLE `products` ADD COLUMN `listing_message` text NOT NULL DEFAULT '';
