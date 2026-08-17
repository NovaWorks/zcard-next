-- Modify "payments" table
ALTER TABLE "payments" ADD COLUMN "charged_currency" character varying NULL, ADD COLUMN "exchange_rate" numeric(20,8) NOT NULL DEFAULT 0, ADD COLUMN "charged_units" bigint NOT NULL DEFAULT 0;
