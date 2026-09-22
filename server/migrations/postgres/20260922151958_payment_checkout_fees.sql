-- Modify "payment_channels" table
ALTER TABLE "payment_channels" ADD COLUMN "recommended" boolean NOT NULL DEFAULT false, ADD COLUMN "recommend_label" character varying NOT NULL DEFAULT '', ADD COLUMN "recommend_description" character varying NOT NULL DEFAULT '';
-- Modify "payments" table
ALTER TABLE "payments" ADD COLUMN "pricing_snapshot" jsonb NULL;
-- Modify "refund_orders" table
ALTER TABLE "refund_orders" ADD COLUMN "fee_amount" bigint NOT NULL DEFAULT 0;
