ALTER TABLE `supplier_product_prices` ADD COLUMN `scope` varchar(255) NOT NULL DEFAULT 'product';
ALTER TABLE `supplier_product_prices` ADD COLUMN `category_id` bigint unsigned NOT NULL DEFAULT 0;
ALTER TABLE `supplier_product_prices` ADD COLUMN `discount_bps` integer NOT NULL DEFAULT 0;
DROP INDEX `supplierproductprice_supplier_account_id_product_id_sku_id` ON `supplier_product_prices`;
CREATE UNIQUE INDEX `supplier_price_scope_unique` ON `supplier_product_prices` (`supplier_account_id`, `scope`, `product_id`, `sku_id`, `category_id`);
