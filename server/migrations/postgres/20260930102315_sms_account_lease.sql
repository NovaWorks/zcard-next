-- Modify "supply_connections" table
ALTER TABLE "supply_connections" ADD COLUMN "sms_lease_token" character varying NOT NULL DEFAULT '', ADD COLUMN "sms_lease_until" bigint NOT NULL DEFAULT 0;
