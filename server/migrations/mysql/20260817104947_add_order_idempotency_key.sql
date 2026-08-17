-- Modify "orders" table
ALTER TABLE `orders` ADD COLUMN `idempotency_key` varchar(80) NULL, ADD UNIQUE INDEX `idempotency_key` (`idempotency_key`);
