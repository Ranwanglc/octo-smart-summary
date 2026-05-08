-- +migrate Up
ALTER TABLE summary_personal_result
    ADD COLUMN edited_at DATETIME DEFAULT NULL COMMENT '最后编辑时间';

ALTER TABLE summary_result
    ADD COLUMN edited_at DATETIME DEFAULT NULL COMMENT '最后编辑时间';

-- +migrate Down
ALTER TABLE summary_personal_result DROP COLUMN edited_at;
ALTER TABLE summary_result DROP COLUMN edited_at;
