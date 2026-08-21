-- Add column "price_markup_amount" to table: "supply_connections"
ALTER TABLE "supply_connections" ADD COLUMN "price_markup_amount" bigint NOT NULL DEFAULT 0;
