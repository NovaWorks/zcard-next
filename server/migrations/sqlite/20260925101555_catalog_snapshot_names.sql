-- Create "supply_catalog_snapshots" table
CREATE TABLE `supply_catalog_snapshots` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `token` text NOT NULL, `connection_id` integer NOT NULL, `identity` text NOT NULL, `status` text NOT NULL DEFAULT ('pending'), `lease_token` text NOT NULL DEFAULT (''), `lease_until` integer NOT NULL DEFAULT (0), `expires_at` integer NOT NULL, `attempts` integer NOT NULL DEFAULT (0), `loaded_count` integer NOT NULL DEFAULT (0), `message` text NOT NULL DEFAULT (''), `payload` json NULL);
-- Create index "supply_catalog_snapshots_token_key" to table: "supply_catalog_snapshots"
CREATE UNIQUE INDEX `supply_catalog_snapshots_token_key` ON `supply_catalog_snapshots` (`token`);
-- Create index "supplycatalogsnapshot_connection_id_subsite_id_created_at" to table: "supply_catalog_snapshots"
CREATE INDEX `supplycatalogsnapshot_connection_id_subsite_id_created_at` ON `supply_catalog_snapshots` (`connection_id`, `subsite_id`, `created_at`);
-- Create index "supplycatalogsnapshot_expires_at" to table: "supply_catalog_snapshots"
CREATE INDEX `supplycatalogsnapshot_expires_at` ON `supply_catalog_snapshots` (`expires_at`);
-- Create index "supplycatalogsnapshot_status_lease_until" to table: "supply_catalog_snapshots"
CREATE INDEX `supplycatalogsnapshot_status_lease_until` ON `supply_catalog_snapshots` (`status`, `lease_until`);
