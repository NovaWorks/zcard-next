-- Create "failed_tasks" table
CREATE TABLE `failed_tasks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `task_type` text NOT NULL, `payload` blob NULL, `error` text NULL, `retry_count` integer NOT NULL DEFAULT (0), `status` text NOT NULL DEFAULT ('pending'), `created_at` datetime NOT NULL);
-- Create index "failedtask_status_created_at" to table: "failed_tasks"
CREATE INDEX `failedtask_status_created_at` ON `failed_tasks` (`status`, `created_at`);
-- Create index "failedtask_task_type" to table: "failed_tasks"
CREATE INDEX `failedtask_task_type` ON `failed_tasks` (`task_type`);
