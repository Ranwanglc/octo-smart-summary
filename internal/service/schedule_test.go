package service

import (
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/timezone"
)

// bj builds a Beijing (Asia/Shanghai) wall-clock time. All scheduling math is
// defined in Asia/Shanghai, so tests pin the location explicitly rather than
// relying on the host TZ.
func bj(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, timezone.Location())
}

// ---------------------------------------------------------------------------
// Validation: recurrence source mutual-exclusivity and bounds.
// ---------------------------------------------------------------------------

func TestValidateInterval_SourceExclusivityAndBounds(t *testing.T) {
	cases := []struct {
		name    string
		cron    string
		days    int
		months  int
		wantErr bool
	}{
		{"cron only", "0 9 * * *", 0, 0, false},
		{"days only", "", 14, 0, false},
		{"months only", "", 0, 1, false},
		{"none provided", "", 0, 0, true},
		{"days+months conflict", "", 7, 1, true},
		{"cron+days conflict", "0 9 * * *", 7, 0, true},
		{"cron+months conflict", "0 9 * * *", 0, 1, true},
		{"negative days", "", -1, 0, true},
		{"negative months", "", 0, -1, true},
		{"days over max", "", MaxIntervalDays + 1, 0, true},
		{"months over max", "", 0, MaxIntervalMonths + 1, true},
		{"days at max", "", MaxIntervalDays, 0, false},
		{"months at max", "", 0, MaxIntervalMonths, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateInterval(c.cron, c.days, c.months)
			if (err != nil) != c.wantErr {
				t.Fatalf("ValidateInterval(%q,%d,%d) err=%v, wantErr=%v", c.cron, c.days, c.months, err, c.wantErr)
			}
		})
	}
}

// ValidateIntervalForWrite is the stricter create/update gate: cron writes are
// rejected (cron is legacy read/execute-only) and exactly one interval source
// must be present.
func TestValidateIntervalForWrite_RejectsCronAndEmpty(t *testing.T) {
	if err := ValidateIntervalForWrite("0 9 * * *", 0, 0); err == nil {
		t.Fatal("expected cron write to be rejected")
	}
	if err := ValidateIntervalForWrite("", 0, 0); err == nil {
		t.Fatal("expected empty interval write to be rejected")
	}
	if err := ValidateIntervalForWrite("", 7, 0); err != nil {
		t.Fatalf("weekly interval write should pass, got %v", err)
	}
	if err := ValidateIntervalForWrite("", 0, 1); err != nil {
		t.Fatalf("monthly interval write should pass, got %v", err)
	}
}

func TestValidateRunTime(t *testing.T) {
	valid := []string{"", "00:00", "09:30", "23:59"}
	for _, v := range valid {
		if err := ValidateRunTime(v); err != nil {
			t.Errorf("ValidateRunTime(%q) unexpected err: %v", v, err)
		}
	}
	invalid := []string{"9:30", "09:60", "24:00", "0930", "09-30", "ab:cd", "09:5"}
	for _, v := range invalid {
		if err := ValidateRunTime(v); err == nil {
			t.Errorf("ValidateRunTime(%q) expected error, got nil", v)
		}
	}
}

func TestValidateScheduleAnchors_ModeMatching(t *testing.T) {
	cases := []struct {
		name    string
		cron    string
		days    int
		months  int
		dow     int
		dom     int
		wantErr bool
	}{
		{"week mode with dow ok", "", 14, 0, 3, 0, false},
		{"day mode with dom rejected", "", 3, 0, 0, 5, true},
		{"non-week day mode with dow rejected", "", 3, 0, 2, 0, true},
		{"month mode with dom ok", "", 0, 1, 0, 15, false},
		{"month mode with dow rejected", "", 0, 1, 2, 0, true},
		{"cron mode with anchors rejected", "0 9 * * *", 0, 0, 1, 0, true},
		{"cron mode without anchors ok", "0 9 * * *", 0, 0, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateScheduleAnchors(c.cron, c.days, c.months, c.dow, c.dom)
			if (err != nil) != c.wantErr {
				t.Fatalf("got err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NextRunWithInterval: ADVANCE form always steps a full interval past `from`.
// ---------------------------------------------------------------------------

func TestNextRunWithInterval_DayAndWeek(t *testing.T) {
	from := bj(2026, time.June, 1, 9, 0)

	// Plain day interval keeps the time-of-day from `from` when runTime empty.
	got, err := NextRunWithInterval("", 3, 0, "", 0, 0, from)
	if err != nil {
		t.Fatal(err)
	}
	if want := bj(2026, time.June, 4, 9, 0); !got.Equal(want) {
		t.Errorf("day interval: got %v want %v", got, want)
	}

	// runTime overrides the time-of-day.
	got, _ = NextRunWithInterval("", 1, 0, "18:30", 0, 0, from)
	if want := bj(2026, time.June, 2, 18, 30); !got.Equal(want) {
		t.Errorf("day interval runTime: got %v want %v", got, want)
	}

	// Bi-weekly (14d) aligned to Wednesday(=3). 2026-06-01 is Monday; +14d =
	// 2026-06-15 (also Monday), aligned forward to Wed 2026-06-17.
	got, _ = NextRunWithInterval("", 14, 0, "", 3, 0, from)
	if want := bj(2026, time.June, 17, 9, 0); !got.Equal(want) {
		t.Errorf("biweekly dow: got %v want %v", got, want)
	}
}

func TestNextRunWithInterval_MonthEndClamp(t *testing.T) {
	// Jan 31 + 1 month must clamp to Feb 28 (2026 is not a leap year), not
	// overflow into March.
	from := bj(2026, time.January, 31, 10, 0)
	got, err := NextRunWithInterval("", 0, 1, "", 0, 0, from)
	if err != nil {
		t.Fatal(err)
	}
	if want := bj(2026, time.February, 28, 10, 0); !got.Equal(want) {
		t.Errorf("month-end clamp: got %v want %v", got, want)
	}

	// Leap year: Jan 31 2028 + 1 month -> Feb 29 2028.
	fromLeap := bj(2028, time.January, 31, 10, 0)
	got, _ = NextRunWithInterval("", 0, 1, "", 0, 0, fromLeap)
	if want := bj(2028, time.February, 29, 10, 0); !got.Equal(want) {
		t.Errorf("leap month-end clamp: got %v want %v", got, want)
	}
}

func TestNextRunWithInterval_InvalidRejected(t *testing.T) {
	if _, err := NextRunWithInterval("", 7, 1, "", 0, 0, bj(2026, time.June, 1, 9, 0)); err == nil {
		t.Fatal("expected dual-source interval to be rejected")
	}
}

// ---------------------------------------------------------------------------
// NextRunInitial: the FIRST run may fire today/this period when the chosen
// time-of-day is still ahead of `from`, otherwise advances one interval.
// ---------------------------------------------------------------------------

func TestNextRunInitial_DayMode(t *testing.T) {
	// run_time 18:00 still ahead of 09:00 today -> fires today.
	from := bj(2026, time.June, 1, 9, 0)
	got, _ := NextRunInitial("", 1, 0, "18:00", 0, 0, from)
	if want := bj(2026, time.June, 1, 18, 0); !got.Equal(want) {
		t.Errorf("day today: got %v want %v", got, want)
	}

	// run_time 08:00 already passed at 09:00 -> advance one day.
	got, _ = NextRunInitial("", 1, 0, "08:00", 0, 0, from)
	if want := bj(2026, time.June, 2, 8, 0); !got.Equal(want) {
		t.Errorf("day passed: got %v want %v", got, want)
	}
}

func TestNextRunInitial_WeekMode(t *testing.T) {
	// 2026-06-01 is Monday(=1). Weekly on Wednesday(=3) at 10:00 -> this week
	// Wednesday 2026-06-03.
	from := bj(2026, time.June, 1, 9, 0)
	got, _ := NextRunInitial("", 7, 0, "10:00", 3, 0, from)
	if want := bj(2026, time.June, 3, 10, 0); !got.Equal(want) {
		t.Errorf("week dow this week: got %v want %v", got, want)
	}

	// Same weekday as `from` but the time already passed -> advance a full
	// week, not a partial +7 from the wrong base.
	fromWed := bj(2026, time.June, 3, 12, 0)
	got, _ = NextRunInitial("", 7, 0, "10:00", 3, 0, fromWed)
	if want := bj(2026, time.June, 10, 10, 0); !got.Equal(want) {
		t.Errorf("week dow same-day passed: got %v want %v", got, want)
	}
}

func TestNextRunInitial_MultiWeekNoCadenceSkew(t *testing.T) {
	// Bi-weekly (14d) on Wednesday, starting on a Wednesday whose time passed.
	// The next run must be exactly 14 days later (same weekday), proving the
	// multi-week cadence is not skewed by a stray +7.
	fromWed := bj(2026, time.June, 3, 12, 0)
	got, _ := NextRunInitial("", 14, 0, "10:00", 3, 0, fromWed)
	if want := bj(2026, time.June, 17, 10, 0); !got.Equal(want) {
		t.Errorf("biweekly cadence: got %v want %v", got, want)
	}
}

func TestNextRunInitial_MonthMode(t *testing.T) {
	// Monthly on the 15th at 09:00; from is the 1st -> this month's 15th.
	from := bj(2026, time.June, 1, 9, 0)
	got, _ := NextRunInitial("", 0, 1, "09:00", 0, 15, from)
	if want := bj(2026, time.June, 15, 9, 0); !got.Equal(want) {
		t.Errorf("month this month: got %v want %v", got, want)
	}

	// Past the 15th -> next month's 15th.
	fromLate := bj(2026, time.June, 20, 9, 0)
	got, _ = NextRunInitial("", 0, 1, "09:00", 0, 15, fromLate)
	if want := bj(2026, time.July, 15, 9, 0); !got.Equal(want) {
		t.Errorf("month next month: got %v want %v", got, want)
	}

	// DOM 31 in a 30-day month clamps to the 30th.
	fromShort := bj(2026, time.September, 1, 9, 0)
	got, _ = NextRunInitial("", 0, 1, "09:00", 0, 31, fromShort)
	if want := bj(2026, time.September, 30, 9, 0); !got.Equal(want) {
		t.Errorf("month dom clamp: got %v want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// NextRunScheduledAdvance: post-run recompute anchors on the ORIGINAL due time
// so a late scan does not skip occurrences and cadence/phase are preserved.
// ---------------------------------------------------------------------------

func TestNextRunScheduledAdvance_LateScanNoSkip(t *testing.T) {
	// Weekly schedule whose anchor was 2026-06-03 10:00; the scheduler only ran
	// at 2026-06-04 (late). Next run must be the very next weekly slot
	// (2026-06-10 10:00), i.e. advance exactly one period past the anchor, not
	// jump multiple weeks from `now`.
	anchor := bj(2026, time.June, 3, 10, 0)
	now := bj(2026, time.June, 4, 0, 0)
	got, err := NextRunScheduledAdvance("", 7, 0, "10:00", 3, 0, 0, anchor, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := bj(2026, time.June, 10, 10, 0); !got.Equal(want) {
		t.Errorf("late scan: got %v want %v", got, want)
	}
}

func TestNextRunScheduledAdvance_MultiPeriodDowntime(t *testing.T) {
	// Daily schedule, anchor 2026-06-01 09:00, scanner down until 2026-06-05.
	// Advance must land on the first slot strictly after `now`: 2026-06-06.
	anchor := bj(2026, time.June, 1, 9, 0)
	now := bj(2026, time.June, 5, 12, 0)
	got, _ := NextRunScheduledAdvance("", 1, 0, "09:00", 0, 0, 0, anchor, now)
	if want := bj(2026, time.June, 6, 9, 0); !got.Equal(want) {
		t.Errorf("multi-period downtime: got %v want %v", got, want)
	}
}

func TestNextRunScheduledAdvance_MonthClampDoesNotDrift(t *testing.T) {
	// Monthly on the 31st (anchorDOM=31), anchor Jan 31. Stepping must produce
	// Feb 28 then Mar 31 (recomputing the day from the stored anchor each step),
	// proving a short-month clamp is never carried forward as a permanent 28th.
	anchor := bj(2026, time.January, 31, 9, 0)
	now := bj(2026, time.February, 1, 0, 0)
	feb, _ := NextRunScheduledAdvance("", 0, 1, "09:00", 0, 0, 31, anchor, now)
	if want := bj(2026, time.February, 28, 9, 0); !feb.Equal(want) {
		t.Fatalf("feb step: got %v want %v", feb, want)
	}
	now2 := bj(2026, time.March, 1, 0, 0)
	mar, _ := NextRunScheduledAdvance("", 0, 1, "09:00", 0, 0, 31, anchor, now2)
	if want := bj(2026, time.March, 31, 9, 0); !mar.Equal(want) {
		t.Fatalf("mar step (no drift): got %v want %v", mar, want)
	}
}

// ---------------------------------------------------------------------------
// Anchor DOM helpers.
// ---------------------------------------------------------------------------

func TestEffectiveMonthlyDOM(t *testing.T) {
	// Explicit DOM always wins.
	if got := EffectiveMonthlyDOM(15, 31); got != 15 {
		t.Errorf("explicit dom: got %d want 15", got)
	}
	// DOM unset (0) restores the persisted anchor.
	if got := EffectiveMonthlyDOM(0, 20); got != 20 {
		t.Errorf("anchor restore: got %d want 20", got)
	}
	// DOM unset and anchor invalid -> 0 (unconstrained).
	if got := EffectiveMonthlyDOM(0, 0); got != 0 {
		t.Errorf("both unset: got %d want 0", got)
	}
}

func TestResolveAnchorDOM(t *testing.T) {
	from := bj(2026, time.June, 7, 9, 0)
	if got := ResolveAnchorDOM(15, from); got != 15 {
		t.Errorf("explicit: got %d want 15", got)
	}
	// Unset DOM falls back to from's own day-of-month.
	if got := ResolveAnchorDOM(0, from); got != 7 {
		t.Errorf("fallback: got %d want 7", got)
	}
}

// ---------------------------------------------------------------------------
// ComputeTimeRange: window selection per time_range_type.
// ---------------------------------------------------------------------------

func TestComputeTimeRange_FixedWindows(t *testing.T) {
	now := bj(2026, time.June, 10, 12, 0)
	cases := []struct {
		rangeType int
		wantStart time.Time
	}{
		{1, now.Add(-24 * time.Hour)},
		{2, now.Add(-7 * 24 * time.Hour)},
		{3, now.Add(-30 * 24 * time.Hour)},
	}
	for _, c := range cases {
		start, end, err := ComputeTimeRange(c.rangeType, now, nil, "", 1, 0)
		if err != nil {
			t.Fatalf("type %d: %v", c.rangeType, err)
		}
		if !start.Equal(c.wantStart) || !end.Equal(now) {
			t.Errorf("type %d: got [%v,%v] want [%v,%v]", c.rangeType, start, end, c.wantStart, now)
		}
	}
}

func TestComputeTimeRange_SinceLastRun(t *testing.T) {
	now := bj(2026, time.June, 10, 12, 0)
	last := bj(2026, time.June, 8, 12, 0)
	// With last_run set, the window starts there.
	start, _, err := ComputeTimeRange(4, now, &last, "", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !start.Equal(last) {
		t.Errorf("since-last with lastRun: got %v want %v", start, last)
	}
	// Without last_run, fall back one interval (1 day here).
	start, _, _ = ComputeTimeRange(4, now, nil, "", 1, 0)
	if want := now.Add(-24 * time.Hour); !start.Equal(want) {
		t.Errorf("since-last no lastRun: got %v want %v", start, want)
	}
}
