-- Add column "product_kind" to table: "products"
ALTER TABLE `products` ADD COLUMN `product_kind` text NOT NULL DEFAULT 'standard';
-- Create "sms_retail_quotes" table
CREATE TABLE `sms_retail_quotes` (`id` text NOT NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `user_id` integer NOT NULL, `product_id` integer NOT NULL, `connection_id` integer NOT NULL, `connection_identity` text NOT NULL, `pricing_revision` text NOT NULL, `product_revision` integer NOT NULL, `upstream_quote_id` text NOT NULL, `upstream_product_id` text NOT NULL, `cost_cents` integer NOT NULL, `amount_cents` integer NOT NULL, `expires_at` integer NOT NULL, `offer_name` text NOT NULL, `selection` json NULL, `consumed_by` text NOT NULL DEFAULT (''), PRIMARY KEY (`id`));
-- Create index "smsretailquote_subsite_id_user_id_product_id" to table: "sms_retail_quotes"
CREATE INDEX `smsretailquote_subsite_id_user_id_product_id` ON `sms_retail_quotes` (`subsite_id`, `user_id`, `product_id`);
-- Create index "smsretailquote_expires_at" to table: "sms_retail_quotes"
CREATE INDEX `smsretailquote_expires_at` ON `sms_retail_quotes` (`expires_at`);
