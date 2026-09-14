-- Modify "admin_users" table
ALTER TABLE "admin_users" ADD COLUMN "auth_version" bigint NOT NULL DEFAULT 0, ADD COLUMN "mfa_revision" bigint NOT NULL DEFAULT 0, ADD COLUMN "mfa_state" character varying NOT NULL DEFAULT '{}';
-- Modify "sessions" table
ALTER TABLE "sessions" ADD COLUMN "auth_version" bigint NOT NULL DEFAULT 0;
