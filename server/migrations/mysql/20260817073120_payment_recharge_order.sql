-- Modify "payments" table
ALTER TABLE `payments` DROP FOREIGN KEY `payments_orders_payments`;
-- Modify "payments" table
ALTER TABLE `payments` MODIFY COLUMN `order_id` bigint unsigned NULL, ADD COLUMN `recharge_order_id` bigint unsigned NULL, ADD CONSTRAINT `payments_orders_payments` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL;
