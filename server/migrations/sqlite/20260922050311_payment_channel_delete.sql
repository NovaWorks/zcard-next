-- Add column "deleted_at" to table: "payment_channels"
ALTER TABLE `payment_channels` ADD COLUMN `deleted_at` datetime NULL;
