package sql

import (
	"strings"
	"testing"

	migrate "github.com/rubenv/sql-migrate"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var pr62Round15BannedConditionalDDL = []string{
	"ADD COLUMN IF NOT EXISTS",
	"ADD UNIQUE KEY IF NOT EXISTS",
	"CREATE UNIQUE INDEX IF NOT EXISTS",
	"CREATE INDEX IF NOT EXISTS",
	"DROP INDEX IF EXISTS",
}

func assertNoMySQLConditionalDDL(t *testing.T, body string) {
	t.Helper()
	upper := strings.ToUpper(body)
	for _, banned := range pr62Round15BannedConditionalDDL {
		if strings.Contains(upper, banned) {
			t.Fatalf("migration must not use MySQL-unsupported conditional DDL %q", banned)
		}
	}
}

func TestPR62Round15_TaskScheduleBinding_ShapeAndSemantics(t *testing.T) {
	raw, err := FS.ReadFile("20260609-01-task-schedule-binding.sql")
	if err != nil {
		t.Fatalf("read 20260609-01: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"UPDATE summary_task t",
		"MIN(id) AS keep_id",
		"information_schema.COLUMNS",
		"information_schema.STATISTICS",
		"PREPARE stmt FROM @pr62_r15_task_binding_sql;",
		"EXECUTE stmt;",
		"DEALLOCATE PREPARE stmt;",
		"ADD COLUMN live_schedule_id BIGINT",
		"ADD UNIQUE KEY uk_live_schedule_binding (live_schedule_id)",
		"DROP INDEX uk_live_schedule_binding",
		"DROP COLUMN live_schedule_id",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("20260609-01 missing %q", want)
		}
	}
	assertNoMySQLConditionalDDL(t, body)

	mig, err := migrate.ParseMigration("20260609-01-task-schedule-binding.sql", strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse 20260609-01: %v", err)
	}
	if len(mig.Up) != 11 {
		t.Fatalf("20260609-01 up statement count=%d want 11", len(mig.Up))
	}
	if len(mig.Down) != 2 {
		t.Fatalf("20260609-01 down statement count=%d want 2", len(mig.Down))
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE summary_task (
		id INTEGER PRIMARY KEY,
		schedule_id INTEGER,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.Exec(`INSERT INTO summary_task (id, schedule_id, deleted_at) VALUES
		(1, 1, NULL), (2, 1, NULL), (3, 1, NULL),
		(4, 2, NULL),
		(5, 3, NULL), (6, 3, '2026-01-01')`).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	heal := `UPDATE summary_task SET schedule_id = NULL
		WHERE deleted_at IS NULL AND schedule_id IS NOT NULL
		  AND id NOT IN (
			SELECT MIN(id) FROM summary_task
			WHERE deleted_at IS NULL AND schedule_id IS NOT NULL
			GROUP BY schedule_id
		  )`
	if err := db.Exec(heal).Error; err != nil {
		t.Fatalf("heal: %v", err)
	}
	if err := db.Exec(heal).Error; err != nil {
		t.Fatalf("heal rerun: %v", err)
	}

	var kept int64
	if err := db.Raw(`SELECT id FROM summary_task WHERE deleted_at IS NULL AND schedule_id = 1`).Scan(&kept).Error; err != nil {
		t.Fatalf("load kept row: %v", err)
	}
	if kept != 1 {
		t.Fatalf("schedule 1 kept id=%d want 1", kept)
	}
	for _, tc := range []struct {
		scheduleID int64
		want       int64
	}{
		{scheduleID: 1, want: 1},
		{scheduleID: 2, want: 1},
		{scheduleID: 3, want: 1},
	} {
		var n int64
		if err := db.Raw(`SELECT COUNT(*) FROM summary_task WHERE deleted_at IS NULL AND schedule_id = ?`, tc.scheduleID).Scan(&n).Error; err != nil {
			t.Fatalf("count schedule %d: %v", tc.scheduleID, err)
		}
		if n != tc.want {
			t.Fatalf("schedule %d live count=%d want %d", tc.scheduleID, n, tc.want)
		}
	}
}

func TestPR62Round15_ScheduleAnchorAndConfig_ShapeAndSemantics(t *testing.T) {
	raw, err := FS.ReadFile("20260609-02-schedule-anchor-and-config.sql")
	if err != nil {
		t.Fatalf("read 20260609-02: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"information_schema.COLUMNS",
		"PREPARE stmt FROM @pr62_r15_schedule_sql;",
		"ADD COLUMN anchor_dom TINYINT NOT NULL DEFAULT 0 AFTER day_of_month",
		"SET anchor_dom = day_of_month",
		"day_of_month BETWEEN 1 AND 31",
		"participant_config = NULL",
		"JSON_TABLE",
		"creator_id",
		"DROP COLUMN anchor_dom",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("20260609-02 missing %q", want)
		}
	}
	if strings.Contains(body, "SET anchor_dom = 0") {
		t.Fatalf("20260609-02 down must not blindly zero anchor_dom")
	}
	assertNoMySQLConditionalDDL(t, body)

	mig, err := migrate.ParseMigration("20260609-02-schedule-anchor-and-config.sql", strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse 20260609-02: %v", err)
	}
	if len(mig.Up) != 7 {
		t.Fatalf("20260609-02 up statement count=%d want 7", len(mig.Up))
	}
	if len(mig.Down) != 1 {
		t.Fatalf("20260609-02 down statement count=%d want 1", len(mig.Down))
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE summary_schedule (
		id INTEGER PRIMARY KEY,
		day_of_month INTEGER NOT NULL DEFAULT 0,
		next_run_at DATETIME,
		anchor_dom INTEGER NOT NULL DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.Exec(`INSERT INTO summary_schedule (id, day_of_month, next_run_at, anchor_dom) VALUES
		(1, 30, '2026-02-28 09:00:00', 0),
		(2, 0,  '2026-02-28 09:00:00', 0),
		(3, 31, '2026-02-28 09:00:00', 0),
		(4, 0,  NULL, 0)`).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	backfill := `UPDATE summary_schedule
		SET anchor_dom = day_of_month
		WHERE anchor_dom = 0
		  AND day_of_month BETWEEN 1 AND 31`
	if err := db.Exec(backfill).Error; err != nil {
		t.Fatalf("backfill: %v", err)
	}

	type row struct {
		ID        int
		AnchorDOM int
	}
	var rows []row
	if err := db.Raw(`SELECT id, anchor_dom FROM summary_schedule ORDER BY id`).Scan(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	want := map[int]int{1: 30, 2: 0, 3: 31, 4: 0}
	for _, row := range rows {
		if row.AnchorDOM != want[row.ID] {
			t.Fatalf("id=%d anchor_dom=%d want %d", row.ID, row.AnchorDOM, want[row.ID])
		}
	}

	creator := "creator1"
	isDirty := func(cfg []map[string]string) bool {
		for _, m := range cfg {
			uid := m["user_id"]
			if uid != "" && uid != creator {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name      string
		cfg       []map[string]string
		wantDirty bool
	}{
		{name: "nil", cfg: nil, wantDirty: false},
		{name: "creator only", cfg: []map[string]string{{"user_id": creator}}, wantDirty: false},
		{name: "empty only", cfg: []map[string]string{{"user_id": ""}}, wantDirty: false},
		{name: "creator and empty", cfg: []map[string]string{{"user_id": creator}, {"user_id": ""}}, wantDirty: false},
		{name: "stranger", cfg: []map[string]string{{"user_id": "stranger"}}, wantDirty: true},
		{name: "creator and stranger", cfg: []map[string]string{{"user_id": creator}, {"user_id": "x"}}, wantDirty: true},
	} {
		if got := isDirty(tc.cfg); got != tc.wantDirty {
			t.Fatalf("%s dirty=%v want %v", tc.name, got, tc.wantDirty)
		}
	}
}

func TestPR62Round15_ParticipantDedup_Shape(t *testing.T) {
	raw, err := FS.ReadFile("20260609-03-participant-dedup-unique.sql")
	if err != nil {
		t.Fatalf("read 20260609-03: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"DELETE sp, pr, sc, ss",
		"ROW_NUMBER() OVER",
		"AS has_content",
		"worker_status = 2",
		"submitted_at IS NOT NULL",
		"pr0.content IS NOT NULL AND pr0.content <> ''",
		"information_schema.STATISTICS",
		"PREPARE stmt FROM @pr62_r15_participant_sql;",
		"CREATE UNIQUE INDEX `uk_summary_participant_task_user` ON `summary_participant` (`task_id`, `user_id`)",
		"DROP INDEX `uk_summary_participant_task_user` ON `summary_participant`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("20260609-03 missing %q", want)
		}
	}
	if strings.Contains(body, "MIN(id) AS keep_id") {
		t.Fatalf("20260609-03 must not regress to keep MIN(id) blindly")
	}
	assertNoMySQLConditionalDDL(t, body)

	mig, err := migrate.ParseMigration("20260609-03-participant-dedup-unique.sql", strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse 20260609-03: %v", err)
	}
	if len(mig.Up) != 6 {
		t.Fatalf("20260609-03 up statement count=%d want 6", len(mig.Up))
	}
	if len(mig.Down) != 1 {
		t.Fatalf("20260609-03 down statement count=%d want 1", len(mig.Down))
	}
}

func TestPR62Round15_ParticipantDedup_KeepByContentAndUniqueIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, ddl := range []string{
		`CREATE TABLE summary_participant (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL, user_id TEXT NOT NULL)`,
		`CREATE TABLE summary_personal_result (
			id INTEGER PRIMARY KEY,
			task_id INTEGER NOT NULL,
			participant_ref_id INTEGER NOT NULL,
			content TEXT,
			worker_status INTEGER NOT NULL DEFAULT 0,
			submitted_at DATETIME
		)`,
		`CREATE TABLE summary_chunk (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL, participant_id INTEGER)`,
		`CREATE TABLE summary_source (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL, participant_id INTEGER)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	for _, seed := range []string{
		`INSERT INTO summary_participant (id, task_id, user_id) VALUES
			(10, 1, 'creator'), (11, 1, 'creator'), (12, 1, 'creator'),
			(20, 2, 'other'), (21, 2, 'other'),
			(30, 3, 'solo')`,
		`INSERT INTO summary_personal_result (id, task_id, participant_ref_id, content, worker_status, submitted_at) VALUES
			(100, 1, 10, '', 0, NULL),
			(101, 1, 11, '', 2, NULL),
			(102, 1, 12, 'final text', 0, NULL),
			(103, 2, 20, '', 0, NULL),
			(104, 2, 21, '', 0, NULL),
			(105, 3, 30, '', 0, NULL)`,
		`INSERT INTO summary_chunk (id, task_id, participant_id) VALUES
			(200, 1, 10), (201, 1, 11), (202, 1, 12),
			(203, 2, 20), (204, 2, 21),
			(205, 3, 30)`,
		`INSERT INTO summary_source (id, task_id, participant_id) VALUES
			(300, 1, 10), (301, 1, 11), (302, 1, 12),
			(303, 2, 20), (304, 2, 21),
			(305, 3, 30)`,
	} {
		if err := db.Exec(seed).Error; err != nil {
			t.Fatalf("seed rows: %v", err)
		}
	}

	rankedQuery := `
		SELECT ranked.id, ranked.task_id, ranked.user_id, ranked.has_content, ranked.rn
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
				SELECT sp.id, sp.task_id, sp.user_id,
					COALESCE(MAX(CASE
						WHEN pr.worker_status = 2
							OR pr.submitted_at IS NOT NULL
							OR (pr.content IS NOT NULL AND pr.content <> '')
						THEN 1 ELSE 0
					END), 0) AS has_content
				FROM summary_participant sp
				LEFT JOIN summary_personal_result pr ON pr.participant_ref_id = sp.id
				GROUP BY sp.id, sp.task_id, sp.user_id
			) scored
		) ranked
		ORDER BY ranked.task_id, ranked.user_id, ranked.rn, ranked.id`

	type rankedRow struct {
		ID         int
		TaskID     int
		UserID     string
		HasContent int
		RN         int
	}
	var ranked []rankedRow
	if err := db.Raw(rankedQuery).Scan(&ranked).Error; err != nil {
		t.Fatalf("rank duplicates: %v", err)
	}
	wantKeep := map[int]int{1: 12, 2: 20, 3: 30}
	for _, row := range ranked {
		if row.RN == 1 && wantKeep[row.TaskID] != row.ID {
			t.Fatalf("task %d keep id=%d want %d", row.TaskID, row.ID, wantKeep[row.TaskID])
		}
	}

	deleteIDsQuery := `SELECT id FROM (` + rankedQuery + `) ranked WHERE ranked.rn > 1`
	var deleteIDs []int
	if err := db.Raw(deleteIDsQuery).Scan(&deleteIDs).Error; err != nil {
		t.Fatalf("load delete ids: %v", err)
	}
	for _, stmt := range []string{
		`DELETE FROM summary_personal_result WHERE participant_ref_id IN (` + deleteIDsQuery + `)`,
		`DELETE FROM summary_chunk WHERE participant_id IN (` + deleteIDsQuery + `)`,
		`DELETE FROM summary_source WHERE participant_id IN (` + deleteIDsQuery + `)`,
		`DELETE FROM summary_participant WHERE id IN (` + deleteIDsQuery + `)`,
		`CREATE UNIQUE INDEX uk_summary_participant_task_user ON summary_participant (task_id, user_id)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("apply stmt: %v", err)
		}
	}

	type participantRow struct {
		ID     int
		TaskID int
		UserID string
	}
	var participants []participantRow
	if err := db.Raw(`SELECT id, task_id, user_id FROM summary_participant ORDER BY task_id, id`).Scan(&participants).Error; err != nil {
		t.Fatalf("load participants: %v", err)
	}
	if len(participants) != 3 {
		t.Fatalf("participant count=%d want 3", len(participants))
	}
	if participants[0].ID != 12 || participants[1].ID != 20 || participants[2].ID != 30 {
		t.Fatalf("participants after heal=%+v want keep ids 12,20,30", participants)
	}

	for _, tc := range []struct {
		table  string
		column string
		want   []int
	}{
		{table: "summary_personal_result", column: "participant_ref_id", want: []int{12, 20, 30}},
		{table: "summary_chunk", column: "participant_id", want: []int{12, 20, 30}},
		{table: "summary_source", column: "participant_id", want: []int{12, 20, 30}},
	} {
		var got []int
		if err := db.Raw("SELECT " + tc.column + " FROM " + tc.table + " ORDER BY id").Scan(&got).Error; err != nil {
			t.Fatalf("load %s: %v", tc.table, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s count=%d want %d", tc.table, len(got), len(tc.want))
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s[%d]=%d want %d", tc.table, i, got[i], tc.want[i])
			}
		}
	}

	if err := db.Exec(`INSERT INTO summary_participant (id, task_id, user_id) VALUES (31, 3, 'solo')`).Error; err == nil {
		t.Fatalf("duplicate (task_id, user_id) insert should fail after unique index")
	}
}
