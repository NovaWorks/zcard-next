-- Drop index "supply_orders_downstream_order_no_key" from table: "supply_orders"
DROP INDEX "supply_orders_downstream_order_no_key";
-- Create index "supplyorder_account_id_downstream_order_no" to table: "supply_orders"
CREATE UNIQUE INDEX "supplyorder_account_id_downstream_order_no" ON "supply_orders" ("account_id", "downstream_order_no");
