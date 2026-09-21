-- Modify "payments" table
ALTER TABLE "payments" ADD COLUMN "gateway_order_ref" character varying NULL, ADD COLUMN "gateway_context" jsonb NULL;
-- Create index "payment_gateway_order_ref" to table: "payments"
CREATE UNIQUE INDEX "payment_gateway_order_ref" ON "payments" ("gateway_order_ref");
