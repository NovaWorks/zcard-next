-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_posts" table
CREATE TABLE `new_posts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `slug` text NOT NULL, `type` text NOT NULL DEFAULT ('blog'), `title_json` json NOT NULL, `summary_json` json NULL, `content_json` text NOT NULL, `thumbnail` text NULL, `category_id` integer NULL, `is_published` bool NOT NULL DEFAULT (false), `published_at` datetime NULL, `sort` integer NOT NULL DEFAULT (0));
-- Copy rows from old table "posts" to new temporary table "new_posts"
INSERT INTO `new_posts` (`id`, `created_at`, `updated_at`, `subsite_id`, `slug`, `type`, `title_json`, `summary_json`, `content_json`, `thumbnail`, `category_id`, `is_published`, `published_at`) SELECT `id`, `created_at`, `updated_at`, `subsite_id`, `slug`, `type`, `title_json`, `summary_json`, `content_json`, `thumbnail`, `category_id`, `is_published`, `published_at` FROM `posts`;
-- Drop "posts" table after copying rows
DROP TABLE `posts`;
-- Rename temporary table "new_posts" to "posts"
ALTER TABLE `new_posts` RENAME TO `posts`;
-- Create index "post_subsite_id_slug" to table: "posts"
CREATE UNIQUE INDEX `post_subsite_id_slug` ON `posts` (`subsite_id`, `slug`);
-- Create index "post_type_is_published" to table: "posts"
CREATE INDEX `post_type_is_published` ON `posts` (`type`, `is_published`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
