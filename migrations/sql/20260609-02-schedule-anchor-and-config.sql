-- +migrate Up
-- Squashed PR#62 schedule migration:
-- 1) add anchor_dom if missing;
-- 2) backfill anchor_dom conservatively from stored day_of_month;
-- 3) clear dirty participant_config payloads that contain non-creator members.
SET @pr62_r15_schedule_has_anchor_dom = (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'summary_schedule'
      AND COLUMN_NAME = 'anchor_dom'
);

SET @pr62_r15_schedule_sql = IF(
    @pr62_r15_schedule_has_anchor_dom = 0,
    'ALTER TABLE summary_schedule
    ADD COLUMN anchor_dom TINYINT NOT NULL DEFAULT 0 AFTER day_of_month',
    'SELECT 1'
);

PREPARE stmt FROM @pr62_r15_schedule_sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- Conservative backfill: only seed anchor_dom from an explicit stored
-- day_of_month. day_of_month=0 means the historical monthly anchor is unknown.
UPDATE summary_schedule
SET anchor_dom = day_of_month
WHERE anchor_dom = 0
  AND day_of_month BETWEEN 1 AND 31;

-- Dirty historical configs expand into multi-person mode once rebound/toggled.
-- Single-person schedules should store NULL, so any non-empty member whose
-- user_id differs from creator_id is cleared.
UPDATE summary_schedule
SET participant_config = NULL
WHERE deleted_at IS NULL
  AND participant_config IS NOT NULL
  AND JSON_VALID(participant_config)
  AND JSON_TYPE(participant_config) = 'ARRAY'
  AND EXISTS (
      SELECT 1
      FROM JSON_TABLE(
          participant_config,
          '$[*]' COLUMNS (uid VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci PATH '$.user_id')
      ) AS jt
      WHERE jt.uid IS NOT NULL
        AND jt.uid <> ''
        AND jt.uid <> summary_schedule.creator_id
  );

-- +migrate Down
-- Down only removes anchor_dom. The backfill is subsumed by dropping the
-- column, and participant_config cleanup is irreversible because the original
-- dirty payloads are not recorded.
ALTER TABLE summary_schedule
    DROP COLUMN anchor_dom;
