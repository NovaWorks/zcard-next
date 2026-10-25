-- Modify "card_imports" table
ALTER TABLE "card_imports" ADD COLUMN "sku_id" bigint NULL, ADD COLUMN "sku_name" character varying NOT NULL DEFAULT '';
