-- +migrate Up
-- Squashed PR#62 task/schedule binding migration:
-- 1) self-heal historical double-bound live tasks by keeping MIN(id);
-- 2) add the generated live_schedule_id column if missing;
-- 3) add the unique live binding index if missing.
--
-- The cleanup keeps the released live-binding semantics: if multiple live
-- tasks point at the same schedule, keep the smallest task id and unbind the
-- rest so the UNIQUE key can be added safely. Re-runnable and deterministic.
UPDATE summary_task t
JOIN (
    SELECT schedule_id, MIN(id) AS keep_id
    FROM summary_task
    WHERE deleted_at IS NULL AND schedule_id IS NOT NULL
    GROUP BY schedule_id
    HAVING COUNT(*) > 1
) keep ON keep.schedule_id = t.schedule_id
SET t.schedule_id = NULL
WHERE t.deleted_at IS NULL
  AND t.schedule_id IS NOT NULL
  AND t.id <> keep.keep_id;

SET @pr62_r15_task_binding_has_live_schedule_id = (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'summary_task'
      AND COLUMN_NAME = 'live_schedule_id'
);

SET @pr62_r15_task_binding_sql = IF(
    @pr62_r15_task_binding_has_live_schedule_id = 0,
    'ALTER TABLE summary_task
    ADD COLUMN live_schedule_id BIGINT
        GENERATED ALWAYS AS (
            CASE WHEN deleted_at IS NULL AND schedule_id IS NOT NULL
                 THEN schedule_id
                 ELSE NULL
            END
        ) STORED',
    'SELECT 1'
);

PREPARE stmt FROM @pr62_r15_task_binding_sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @pr62_r15_task_binding_has_uk_live_schedule_binding = (
    SELECT COUNT(*)
    FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'summary_task'
      AND INDEX_NAME = 'uk_live_schedule_binding'
);

SET @pr62_r15_task_binding_sql = IF(
    @pr62_r15_task_binding_has_uk_live_schedule_binding = 0,
    'ALTER TABLE summary_task
    ADD UNIQUE KEY uk_live_schedule_binding (live_schedule_id)',
    'SELECT 1'
);

PREPARE stmt FROM @pr62_r15_task_binding_sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- +migrate Down
-- The Up unbind is irreversible because the original duplicate schedule_id
-- assignments are not recorded. Down only removes the schema objects.
ALTER TABLE summary_task
    DROP INDEX uk_live_schedule_binding;

ALTER TABLE summary_task
    DROP COLUMN live_schedule_id;
