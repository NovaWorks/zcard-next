-- Modify "procurement_orders" table
ALTER TABLE `procurement_orders` DROP INDEX `procurementorder_order_item_id`;
-- Modify "procurement_orders" table
ALTER TABLE `procurement_orders` ADD UNIQUE INDEX `procurementorder_order_item_id` (`order_item_id`);
