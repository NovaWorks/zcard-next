-- Modify "payment_channels" table
ALTER TABLE "payment_channels" ADD COLUMN "deleted_at" timestamptz NULL;
