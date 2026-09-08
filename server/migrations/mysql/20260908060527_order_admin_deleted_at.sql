-- Modify "orders" table
ALTER TABLE `orders` ADD COLUMN `admin_deleted_at` datetime(3) NULL;
