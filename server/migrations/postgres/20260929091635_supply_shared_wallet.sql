-- Modify "supplier_accounts" table
ALTER TABLE "supplier_accounts" ADD COLUMN "shared_wallet" boolean NOT NULL DEFAULT false;
