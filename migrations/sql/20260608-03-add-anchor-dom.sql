-- +migrate Up
ALTER TABLE summary_schedule
    ADD COLUMN anchor_dom TINYINT NOT NULL DEFAULT 0 AFTER day_of_month;

-- +migrate Down
ALTER TABLE summary_schedule
    DROP COLUMN anchor_dom;
