-- Modify "payments" table
ALTER TABLE `payments` ADD COLUMN `charged_precision` int NOT NULL DEFAULT -1;
