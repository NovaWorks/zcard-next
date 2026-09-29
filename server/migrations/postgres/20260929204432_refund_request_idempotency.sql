-- Modify "refund_orders" table
ALTER TABLE "refund_orders" ADD COLUMN "request_key" character varying NULL, ADD COLUMN "request_hash" character varying NULL;
-- Create index "refund_orders_request_key_key" to table: "refund_orders"
CREATE UNIQUE INDEX "refund_orders_request_key_key" ON "refund_orders" ("request_key");
