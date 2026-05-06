-- Purpose: Mark baseline as already applied on existing production DB.
-- Run ONCE before deploying the new version with sql-migrate.
-- After this, service startup will only execute 006 + 007.

CREATE TABLE IF NOT EXISTS `gorp_migrations` (
    `id` VARCHAR(255) NOT NULL,
    `applied_at` DATETIME NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO `gorp_migrations` (`id`, `applied_at`) VALUES
('20260101-00-baseline.sql', NOW());

-- Note: 20260101-06-batch-indexes.sql and 20260506-01-title-varchar-1000.sql
-- are NOT marked here — they will be auto-applied on first startup.
