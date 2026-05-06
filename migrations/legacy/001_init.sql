-- P1 MySQL Schema for Smart Summary
-- All tables use InnoDB, utf8mb4, unicode_ci

CREATE TABLE IF NOT EXISTS `summary_task` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `task_no` VARCHAR(32) NOT NULL,
  `space_id` VARCHAR(64) NOT NULL DEFAULT '',
  `creator_id` VARCHAR(64) NOT NULL,
  `title` VARCHAR(200) NOT NULL DEFAULT '',
  `summary_mode` TINYINT NOT NULL COMMENT '1=by_group, 2=by_person',
  `time_range_start` DATETIME NOT NULL,
  `time_range_end` DATETIME NOT NULL,
  `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=pending,1=waiting_confirm,2=processing,3=completed,4=failed,5=cancelled',
  `trigger_type` TINYINT NOT NULL DEFAULT 1 COMMENT '1=manual,2=scheduled',
  `retry_count` TINYINT NOT NULL DEFAULT 0,
  `error_message` VARCHAR(500) DEFAULT NULL,
  `schedule_id` BIGINT DEFAULT NULL,
  `processing_deadline` DATETIME DEFAULT NULL,
  `confirm_deadline` DATETIME DEFAULT NULL,
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_task_no` (`task_no`),
  KEY `idx_space_status` (`space_id`, `status`),
  KEY `idx_status_retry` (`status`, `retry_count`),
  KEY `idx_schedule_id` (`schedule_id`),
  KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `summary_source` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `task_id` BIGINT NOT NULL,
  `source_type` TINYINT NOT NULL COMMENT '1=group,2=thread,3=direct',
  `source_id` VARCHAR(64) NOT NULL,
  `source_name` VARCHAR(200) NOT NULL DEFAULT '',
  `participant_id` BIGINT DEFAULT NULL,
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_task_id` (`task_id`),
  KEY `idx_participant_id` (`participant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `summary_participant` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `task_id` BIGINT NOT NULL,
  `user_id` VARCHAR(64) NOT NULL,
  `user_name` VARCHAR(100) NOT NULL DEFAULT '',
  `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=pending,1=confirmed',
  `confirmed_at` DATETIME DEFAULT NULL,
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_task_id` (`task_id`),
  KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `summary_chunk` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `task_id` BIGINT NOT NULL,
  `chunk_index` INT NOT NULL,
  `participant_id` BIGINT DEFAULT NULL,
  `summary_source_id` BIGINT DEFAULT NULL,
  `msg_count` INT NOT NULL DEFAULT 0,
  `msg_start_time` DATETIME DEFAULT NULL,
  `msg_end_time` DATETIME DEFAULT NULL,
  `chunk_summary` MEDIUMTEXT NOT NULL,
  `token_used` INT NOT NULL DEFAULT 0,
  `status` TINYINT NOT NULL DEFAULT 0,
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_task_id` (`task_id`),
  KEY `idx_summary_source_id` (`summary_source_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `summary_result` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `task_id` BIGINT NOT NULL,
  `content` MEDIUMTEXT NOT NULL,
  `total_msg_count` INT NOT NULL DEFAULT 0,
  `total_token_used` INT NOT NULL DEFAULT 0,
  `model_version` VARCHAR(50) NOT NULL DEFAULT '',
  `version` INT NOT NULL DEFAULT 1,
  `generated_at` DATETIME NOT NULL,
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_task_id` (`task_id`),
  KEY `idx_task_version` (`task_id`, `version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `summary_schedule` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `space_id` VARCHAR(64) NOT NULL DEFAULT '',
  `creator_id` VARCHAR(64) NOT NULL,
  `title` VARCHAR(200) NOT NULL DEFAULT '',
  `summary_mode` TINYINT NOT NULL,
  `cron_expr` VARCHAR(50) NOT NULL,
  `time_range_type` TINYINT NOT NULL COMMENT '1=24h,2=7d,3=30d,4=since_last_run',
  `source_config` JSON DEFAULT NULL,
  `participant_config` JSON DEFAULT NULL,
  `is_active` TINYINT NOT NULL DEFAULT 1,
  `last_run_at` DATETIME DEFAULT NULL,
  `next_run_at` DATETIME DEFAULT NULL,
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_space_active` (`space_id`, `is_active`),
  KEY `idx_next_run` (`is_active`, `next_run_at`),
  KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `summary_event` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `task_id` BIGINT NOT NULL,
  `status` TINYINT NOT NULL,
  `progress` TINYINT NOT NULL DEFAULT 0,
  `message` VARCHAR(200) NOT NULL DEFAULT '',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_task_id` (`task_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
