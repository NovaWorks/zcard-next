-- Add column "admin_deleted_at" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `admin_deleted_at` datetime NULL;
