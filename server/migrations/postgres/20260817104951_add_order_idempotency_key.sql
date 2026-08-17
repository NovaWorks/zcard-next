-- Modify "orders" table
ALTER TABLE "orders" ADD COLUMN "idempotency_key" character varying NULL;
-- Create index "orders_idempotency_key_key" to table: "orders"
CREATE UNIQUE INDEX "orders_idempotency_key_key" ON "orders" ("idempotency_key");
