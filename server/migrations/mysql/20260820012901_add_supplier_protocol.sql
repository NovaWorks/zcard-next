-- Add columns "protocol", "display_name" to table: "supplier_accounts"
ALTER TABLE `supplier_accounts` ADD COLUMN `protocol` enum('zcard','dujiao_next','acg_faka') NOT NULL DEFAULT 'zcard', ADD COLUMN `display_name` varchar(100) NULL;
