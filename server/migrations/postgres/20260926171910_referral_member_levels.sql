-- Modify "users" table
ALTER TABLE "users" ADD COLUMN "referral_level_id" bigint NOT NULL DEFAULT 0, ADD COLUMN "invite_level_id" bigint NOT NULL DEFAULT 0;
-- Create index "user_invite_level_id" to table: "users"
CREATE INDEX "user_invite_level_id" ON "users" ("invite_level_id");
-- Create index "user_referral_level_id" to table: "users"
CREATE INDEX "user_referral_level_id" ON "users" ("referral_level_id");
