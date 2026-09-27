-- Drop index "procurementorder_order_item_id" from table: "procurement_orders"
DROP INDEX "procurementorder_order_item_id";
-- Create index "procurementorder_order_item_id" to table: "procurement_orders"
CREATE UNIQUE INDEX "procurementorder_order_item_id" ON "procurement_orders" ("order_item_id");
