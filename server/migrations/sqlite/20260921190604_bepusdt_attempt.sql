-- Add column "gateway_order_ref" to table: "payments"
ALTER TABLE `payments` ADD COLUMN `gateway_order_ref` text NULL;
-- Add column "gateway_context" to table: "payments"
ALTER TABLE `payments` ADD COLUMN `gateway_context` json NULL;
-- Create index "payment_gateway_order_ref" to table: "payments"
CREATE UNIQUE INDEX `payment_gateway_order_ref` ON `payments` (`gateway_order_ref`);
