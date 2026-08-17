-- Modify "payments" table
ALTER TABLE "payments" DROP CONSTRAINT "payments_orders_payments", ALTER COLUMN "order_id" DROP NOT NULL, ADD COLUMN "recharge_order_id" bigint NULL, ADD CONSTRAINT "payments_orders_payments" FOREIGN KEY ("order_id") REFERENCES "orders" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
