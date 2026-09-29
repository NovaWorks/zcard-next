-- Add column "shared_wallet" to table: "supplier_accounts"
ALTER TABLE `supplier_accounts` ADD COLUMN `shared_wallet` bool NOT NULL DEFAULT false;
