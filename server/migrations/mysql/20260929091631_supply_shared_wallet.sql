-- Modify "supplier_accounts" table
ALTER TABLE `supplier_accounts` ADD COLUMN `shared_wallet` bool NOT NULL DEFAULT 0;
