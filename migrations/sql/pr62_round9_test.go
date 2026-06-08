package sql

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPR62Round9_UniqueBindingMigrationRestoredToSingleFile(t *testing.T) {
	body, err := FS.ReadFile("20260608-01-unique-live-schedule-binding.sql")
	if err != nil {
		t.Fatalf("read 01: %v", err)
	}
	for _, want := range []string{"UPDATE summary_task", "ADD COLUMN live_schedule_id", "ADD UNIQUE KEY uk_live_schedule_binding"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("restored 01 missing %q", want)
		}
	}
	for _, removed := range []string{
		"20260608-01a-heal-live-schedule-binding.sql",
		"20260608-01b-add-live-schedule-id.sql",
		"20260608-01c-add-unique-live-schedule-binding.sql",
	} {
		if _, err := FS.ReadFile(removed); err == nil {
			t.Fatalf("%s should be absent after rollback of the unsafe split", removed)
		}
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE summary_task (
		id INTEGER PRIMARY KEY,
		schedule_id INTEGER,
		deleted_at DATETIME,
		live_schedule_id INTEGER
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.Exec(`INSERT INTO summary_task (id, schedule_id, deleted_at) VALUES
		(1, 7, NULL), (2, 7, NULL), (3, 8, NULL)`).Error; err != nil {
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
	if err := db.Exec(`UPDATE summary_task
		SET live_schedule_id = CASE
			WHEN deleted_at IS NULL AND schedule_id IS NOT NULL THEN schedule_id
			ELSE NULL
		END`).Error; err != nil {
		t.Fatalf("populate live_schedule_id: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX uk_live_schedule_binding ON summary_task(live_schedule_id)`).Error; err != nil {
		t.Fatalf("create unique index: %v", err)
	}
	// Rerun-safe from the failed step forward: index recreation works after the
	// earlier cleanup/column-population has already committed.
	if err := db.Exec(`DROP INDEX uk_live_schedule_binding`).Error; err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX uk_live_schedule_binding ON summary_task(live_schedule_id)`).Error; err != nil {
		t.Fatalf("recreate unique index: %v", err)
	}
}

func TestPR62Round9_AnchorDOM_BackfillSemanticsAndFiles(t *testing.T) {
	addCol, err := FS.ReadFile("20260608-03-add-anchor-dom.sql")
	if err != nil {
		t.Fatalf("read 03: %v", err)
	}
	if !strings.Contains(string(addCol), "ADD COLUMN anchor_dom") {
		t.Fatalf("03 must add anchor_dom")
	}
	backfill, err := FS.ReadFile("20260608-04-backfill-anchor-dom.sql")
	if err != nil {
		t.Fatalf("read 04: %v", err)
	}
	body := string(backfill)
	for _, want := range []string{"UPDATE summary_schedule", "SET anchor_dom = day_of_month", "day_of_month BETWEEN 1 AND 31"} {
		if !strings.Contains(body, want) {
			t.Fatalf("04 missing %q", want)
		}
	}
	for _, forbidden := range []string{"DAY(next_run_at)", "next_run_at IS NOT NULL"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("04 must not guess anchor_dom from %q", forbidden)
		}
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
	portable := `UPDATE summary_schedule
		SET anchor_dom = day_of_month
		WHERE anchor_dom = 0
		  AND day_of_month BETWEEN 1 AND 31`
	if err := db.Exec(portable).Error; err != nil {
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
}
