-- +migrate Up
CREATE INDEX `idx_participant_task_user` ON `summary_participant` (`user_id`, `task_id`);
CREATE INDEX `idx_event_task_id` ON `summary_event` (`task_id`, `id` DESC);

-- +migrate Down
DROP INDEX `idx_participant_task_user` ON `summary_participant`;
DROP INDEX `idx_event_task_id` ON `summary_event`;
