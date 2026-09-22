-- Modify "payment_channels" table
ALTER TABLE `payment_channels` ADD COLUMN `recommended` bool NOT NULL DEFAULT 0, ADD COLUMN `recommend_label` varchar(24) NOT NULL DEFAULT "", ADD COLUMN `recommend_description` varchar(180) NOT NULL DEFAULT "";
-- Modify "payments" table
ALTER TABLE `payments` ADD COLUMN `pricing_snapshot` json NULL;
-- Modify "refund_orders" table
ALTER TABLE `refund_orders` ADD COLUMN `fee_amount` bigint NOT NULL DEFAULT 0;
