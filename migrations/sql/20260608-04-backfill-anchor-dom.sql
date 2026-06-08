-- +migrate Up
-- Conservative backfill: only seed anchor_dom from an explicit stored
-- day_of_month, which is trustworthy. day_of_month=0 means the historical
-- implicit anchor is unknown; do NOT guess from next_run_at because it may
-- already be month-clamped (for example original 30th drifted to Feb 28).
UPDATE summary_schedule
SET anchor_dom = day_of_month
WHERE anchor_dom = 0
  AND day_of_month BETWEEN 1 AND 31;

-- +migrate Down
UPDATE summary_schedule
SET anchor_dom = 0;
