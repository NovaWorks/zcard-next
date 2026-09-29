-- Modify "refund_orders" table
ALTER TABLE `refund_orders` ADD COLUMN `request_key` varchar(64) NULL, ADD COLUMN `request_hash` varchar(64) NULL, ADD UNIQUE INDEX `request_key` (`request_key`);
