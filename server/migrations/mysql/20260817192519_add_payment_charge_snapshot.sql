-- Modify "payments" table
ALTER TABLE `payments` ADD COLUMN `charged_currency` varchar(8) NULL, ADD COLUMN `exchange_rate` decimal(20,8) NOT NULL DEFAULT 0.00000000, ADD COLUMN `charged_units` bigint NOT NULL DEFAULT 0;
