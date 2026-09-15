-- Modify "payments" table
ALTER TABLE "payments" ADD COLUMN "charged_precision" integer NOT NULL DEFAULT -1;
