-- Modify "payments" table
ALTER TABLE `payments` ADD COLUMN `gateway_order_ref` varchar(64) NULL, ADD COLUMN `gateway_context` json NULL, ADD UNIQUE INDEX `payment_gateway_order_ref` (`gateway_order_ref`);
