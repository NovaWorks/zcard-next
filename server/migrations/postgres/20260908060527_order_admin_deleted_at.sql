-- Modify "orders" table
ALTER TABLE "orders" ADD COLUMN "admin_deleted_at" timestamptz NULL;
