ALTER TABLE payment_channels ADD COLUMN allow_purchase boolean NOT NULL DEFAULT true;
ALTER TABLE payment_channels ADD COLUMN allow_member_recharge boolean NOT NULL DEFAULT true;
ALTER TABLE payment_channels ADD COLUMN allow_supply_recharge boolean NOT NULL DEFAULT true;
