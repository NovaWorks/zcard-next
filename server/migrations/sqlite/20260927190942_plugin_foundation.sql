-- Add column "request_fingerprint" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `request_fingerprint` text NULL;
-- Add column "plugin_decisions" to table: "orders"
ALTER TABLE `orders` ADD COLUMN `plugin_decisions` json NULL;
-- Add column "plugin_rule_revision" to table: "products"
ALTER TABLE `products` ADD COLUMN `plugin_rule_revision` integer NOT NULL DEFAULT 0;
-- Create "installed_plugins" table
CREATE TABLE `installed_plugins` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `plugin_id` text NOT NULL, `desired_enabled` bool NOT NULL DEFAULT (false), `desired_generation` integer NOT NULL DEFAULT (0), `observed_generation` integer NOT NULL DEFAULT (0), `desired_digest` text NOT NULL DEFAULT (''), `observed_digest` text NOT NULL DEFAULT (''), `approved_scopes` json NULL, `block_reasons` json NULL, `current_operation_id` text NOT NULL DEFAULT (''), `uninstalled` bool NOT NULL DEFAULT (false));
-- Create index "installed_plugins_plugin_id_key" to table: "installed_plugins"
CREATE UNIQUE INDEX `installed_plugins_plugin_id_key` ON `installed_plugins` (`plugin_id`);
-- Create "plugin_data" table
CREATE TABLE `plugin_data` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `plugin_id` text NOT NULL, `subsite_id` integer NOT NULL, `entity_type` text NOT NULL, `entity_id` integer NOT NULL, `key` text NOT NULL, `payload` blob NOT NULL, `schema_version` integer NOT NULL DEFAULT (1), `revision` integer NOT NULL DEFAULT (0));
-- Create index "plugindata_plugin_id_subsite_id_entity_type_entity_id_key" to table: "plugin_data"
CREATE UNIQUE INDEX `plugindata_plugin_id_subsite_id_entity_type_entity_id_key` ON `plugin_data` (`plugin_id`, `subsite_id`, `entity_type`, `entity_id`, `key`);
-- Create "plugin_operations" table
CREATE TABLE `plugin_operations` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `operation_id` text NOT NULL, `plugin_id` text NOT NULL, `action` text NOT NULL, `request_sha256` text NOT NULL, `actor_id` integer NOT NULL, `subsite_id` integer NOT NULL, `expected_generation` integer NOT NULL, `target_generation` integer NOT NULL DEFAULT (0), `target_digest` text NOT NULL DEFAULT (''), `approved_scopes` json NULL, `phase` text NOT NULL, `failure_code` text NOT NULL DEFAULT (''));
-- Create index "plugin_operations_operation_id_key" to table: "plugin_operations"
CREATE UNIQUE INDEX `plugin_operations_operation_id_key` ON `plugin_operations` (`operation_id`);
-- Create index "pluginoperation_plugin_id_created_at" to table: "plugin_operations"
CREATE INDEX `pluginoperation_plugin_id_created_at` ON `plugin_operations` (`plugin_id`, `created_at`);
-- Create "plugin_requirements" table
CREATE TABLE `plugin_requirements` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `plugin_id` text NOT NULL, `subsite_id` integer NOT NULL, `product_id` integer NOT NULL, `required` bool NOT NULL DEFAULT (false), `revision` integer NOT NULL DEFAULT (0));
-- Create index "pluginrequirement_plugin_id_subsite_id_product_id" to table: "plugin_requirements"
CREATE UNIQUE INDEX `pluginrequirement_plugin_id_subsite_id_product_id` ON `plugin_requirements` (`plugin_id`, `subsite_id`, `product_id`);
-- Create index "pluginrequirement_subsite_id_product_id" to table: "plugin_requirements"
CREATE INDEX `pluginrequirement_subsite_id_product_id` ON `plugin_requirements` (`subsite_id`, `product_id`);
-- Create "plugin_rule_level_refs" table
CREATE TABLE `plugin_rule_level_refs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `plugin_id` text NOT NULL, `subsite_id` integer NOT NULL, `product_id` integer NOT NULL, `level_id` integer NOT NULL);
-- Create index "pluginrulelevelref_plugin_id_subsite_id_product_id_level_id" to table: "plugin_rule_level_refs"
CREATE UNIQUE INDEX `pluginrulelevelref_plugin_id_subsite_id_product_id_level_id` ON `plugin_rule_level_refs` (`plugin_id`, `subsite_id`, `product_id`, `level_id`);
-- Create index "pluginrulelevelref_level_id" to table: "plugin_rule_level_refs"
CREATE INDEX `pluginrulelevelref_level_id` ON `plugin_rule_level_refs` (`level_id`);
