-- +migrate Up
-- Squashed PR#62 participant migration:
-- 1) delete duplicate (task_id,user_id) rows plus dependent personal/chunk/source
--    records, but keep the best participant by content:
--      - prefer any participant whose personal_result is completed/submitted or
--        has non-empty content;
--      - among content-bearing rows keep the largest id;
--      - if no row has content, fall back to MIN(id).
-- 2) add the UNIQUE(task_id,user_id) index if missing.
DELETE sp, pr, sc, ss
FROM summary_participant sp
JOIN (
    SELECT keepers.task_id, keepers.user_id, keepers.keep_id
    FROM (
        SELECT ranked.task_id, ranked.user_id, ranked.id AS keep_id
        FROM (
            SELECT scored.id, scored.task_id, scored.user_id, scored.has_content,
                   ROW_NUMBER() OVER (
                       PARTITION BY scored.task_id, scored.user_id
                       ORDER BY
                           scored.has_content DESC,
                           CASE WHEN scored.has_content = 1 THEN scored.id ELSE NULL END DESC,
                           CASE WHEN scored.has_content = 0 THEN scored.id ELSE NULL END ASC
                   ) AS rn
            FROM (
                SELECT sp0.id, sp0.task_id, sp0.user_id,
                       COALESCE(MAX(CASE
                           WHEN pr0.worker_status = 2
                             OR pr0.submitted_at IS NOT NULL
                             OR (pr0.content IS NOT NULL AND pr0.content <> '')
                           THEN 1 ELSE 0
                       END), 0) AS has_content
                FROM summary_participant sp0
                LEFT JOIN summary_personal_result pr0
                    ON pr0.participant_ref_id = sp0.id
                GROUP BY sp0.id, sp0.task_id, sp0.user_id
            ) scored
        ) ranked
        WHERE ranked.rn = 1
    ) keepers
) keep ON keep.task_id = sp.task_id AND keep.user_id = sp.user_id
LEFT JOIN summary_personal_result pr ON pr.participant_ref_id = sp.id
LEFT JOIN summary_chunk sc ON sc.participant_id = sp.id
LEFT JOIN summary_source ss ON ss.participant_id = sp.id
WHERE sp.id <> keep.keep_id;

SET @pr62_r15_participant_has_uk_summary_participant_task_user = (
    SELECT COUNT(*)
    FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'summary_participant'
      AND INDEX_NAME = 'uk_summary_participant_task_user'
);

SET @pr62_r15_participant_sql = IF(
    @pr62_r15_participant_has_uk_summary_participant_task_user = 0,
    'CREATE UNIQUE INDEX `uk_summary_participant_task_user` ON `summary_participant` (`task_id`, `user_id`)',
    'SELECT 1'
);

PREPARE stmt FROM @pr62_r15_participant_sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- +migrate Down
-- The duplicate-row cleanup is irreversible because removed participant rows
-- and their dependent records are not recoverable from migration state.
DROP INDEX `uk_summary_participant_task_user` ON `summary_participant`;
