-- Modify "supply_orders" table
ALTER TABLE `supply_orders` DROP INDEX `downstream_order_no`, ADD UNIQUE INDEX `supplyorder_account_id_downstream_order_no` (`account_id`, `downstream_order_no`);
