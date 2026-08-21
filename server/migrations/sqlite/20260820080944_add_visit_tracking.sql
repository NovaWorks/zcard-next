-- Create "page_views" table
CREATE TABLE `page_views` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `day` text NOT NULL, `path` text NOT NULL, `user_id` integer NULL, `ip` text NOT NULL);
-- Create index "pageview_day_ip" to table: "page_views"
CREATE INDEX `pageview_day_ip` ON `page_views` (`day`, `ip`);
-- Create index "pageview_day_subsite_id" to table: "page_views"
CREATE INDEX `pageview_day_subsite_id` ON `page_views` (`day`, `subsite_id`);
-- Create "user_sessions" table
CREATE TABLE `user_sessions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `subsite_id` integer NOT NULL DEFAULT (0), `user_id` integer NOT NULL, `ip` text NOT NULL, `last_active_at` datetime NOT NULL);
-- Create index "usersession_subsite_id_user_id" to table: "user_sessions"
CREATE UNIQUE INDEX `usersession_subsite_id_user_id` ON `user_sessions` (`subsite_id`, `user_id`);
-- Create index "usersession_last_active_at" to table: "user_sessions"
CREATE INDEX `usersession_last_active_at` ON `user_sessions` (`last_active_at`);
