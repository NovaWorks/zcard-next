-- Modify "payment_channels" table
ALTER TABLE "payment_channels" ALTER COLUMN "icon" TYPE character varying;
-- Modify "products" table
ALTER TABLE "products" ADD COLUMN "is_recommend" boolean NOT NULL DEFAULT false;
