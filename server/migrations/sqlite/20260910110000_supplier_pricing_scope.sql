ALTER TABLE `supplier_product_prices` ADD COLUMN `scope` text NOT NULL DEFAULT 'product';
ALTER TABLE `supplier_product_prices` ADD COLUMN `category_id` integer NOT NULL DEFAULT 0;
ALTER TABLE `supplier_product_prices` ADD COLUMN `discount_bps` integer NOT NULL DEFAULT 0;
DROP INDEX `supplierproductprice_supplier_account_id_product_id_sku_id`;
CREATE UNIQUE INDEX `supplier_price_scope_unique` ON `supplier_product_prices` (`supplier_account_id`, `scope`, `product_id`, `sku_id`, `category_id`);
