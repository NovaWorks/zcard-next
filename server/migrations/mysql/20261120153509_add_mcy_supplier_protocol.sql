-- Modify "supplier_accounts" table
ALTER TABLE `supplier_accounts` MODIFY COLUMN `protocol` enum('zcard','dujiao_next','acg_faka','mcy_shop') NOT NULL DEFAULT "zcard";
