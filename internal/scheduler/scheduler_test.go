package scheduler

import (
	"math"
	"strings"
	"testing"
	"time"
)

// D builds a local time on 2026-08-20; if wd >= 0, it rolls forward to the
// next date with that weekday (Mon=0).
func D(hh, mm, wd int) time.Time {
	base := time.Date(2026, 8, 20, hh, mm, 0, 0, time.Local)
	if wd >= 0 {
		base = base.AddDate(0, 0, ((wd-Weekday(base))%7+7)%7)
	}
	return base
}

func days(d ...int) *[]int { return &d }

func all() *[]int { return days(0, 1, 2, 3, 4, 5, 6) }

func weekly(start, end string, d *[]int) Schedule {
	return Schedule{Enabled: true, Start: start, End: end, Recurrence: "weekly", Days: d}
}

func check(t *testing.T, got, want bool, label string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v want %v", label, got, want)
	}
}

func TestSameDayWindow(t *testing.T) {
	s := weekly("02:00", "06:00", all())
	check(t, IsWithinWindow(D(3, 0, -1), s), true, "03:00")
	check(t, IsWithinWindow(D(1, 0, -1), s), false, "01:00")
	check(t, IsWithinWindow(D(6, 0, -1), s), false, "end exclusive")
	check(t, IsWithinWindow(D(2, 0, -1), s), true, "start inclusive")
}

func TestMidnightCrossing(t *testing.T) {
	s := weekly("23:00", "05:00", all())
	check(t, IsWithinWindow(D(23, 30, -1), s), true, "before midnight")
	check(t, IsWithinWindow(D(2, 0, -1), s), true, "after midnight")
	check(t, IsWithinWindow(D(12, 0, -1), s), false, "noon")
}

func TestWeekdayFilter(t *testing.T) {
	s := weekly("02:00", "06:00", days(0, 1))
	check(t, IsWithinWindow(D(3, 0, 0), s), true, "Monday")
	check(t, IsWithinWindow(D(3, 0, 2), s), false, "Wednesday")
}

func TestWeekdayFilterMidnightUsesStartDay(t *testing.T) {
	s := weekly("23:00", "05:00", days(4))
	check(t, IsWithinWindow(D(23, 0, 4), s), true, "Fri 23:00")
	check(t, IsWithinWindow(D(2, 0, 5), s), true, "Sat 02:00 == Fri window")
	check(t, IsWithinWindow(D(2, 0, 6), s), false, "Sun 02:00")
}

func TestOnceSchedule(t *testing.T) {
	s := Schedule{Enabled: true, Start: "02:00", End: "06:00", Recurrence: "once", Date: "2026-08-21"}
	check(t, IsWithinWindow(time.Date(2026, 8, 21, 3, 0, 0, 0, time.Local), s), true, "on date")
	check(t, IsWithinWindow(time.Date(2026, 8, 22, 3, 0, 0, 0, time.Local), s), false, "next day")
}

func Test12h24hRoundtrip(t *testing.T) {
	for _, hhmm := range []string{"00:00", "00:30", "02:00", "11:59", "12:00", "12:45", "13:05", "23:59"} {
		h, m, ap := Split12h(hhmm)
		if got := Join24h(h, m, ap); got != hhmm {
			t.Errorf("%s -> %s", hhmm, got)
		}
	}
	if h, m, ap := Split12h("00:15"); h != 12 || m != 15 || ap != "AM" {
		t.Error("00:15")
	}
	if h, m, ap := Split12h("12:15"); h != 12 || m != 15 || ap != "PM" {
		t.Error("12:15")
	}
}

func TestDisabledNeverMatches(t *testing.T) {
	s := weekly("00:00", "23:59", all())
	s.Enabled = false
	check(t, IsWithinWindow(D(12, 0, -1), s), false, "disabled")
}

func TestEmptyDaysNeverActive(t *testing.T) {
	s := weekly("02:00", "06:00", days())
	check(t, IsWithinWindow(D(3, 0, 0), s), false, "Mon")
	check(t, IsWithinWindow(D(3, 0, 3), s), false, "Thu")
}

func TestMissingDaysKeyDefaultsAll(t *testing.T) {
	s := weekly("02:00", "06:00", nil)
	check(t, IsWithinWindow(D(3, 0, 0), s), true, "Mon")
}

func TestSchedulerEdges(t *testing.T) {
	sch := NewOffPeakScheduler(weekly("02:00", "06:00", all()))
	for _, c := range []struct {
		h    int
		want Edge
	}{{1, EdgeNone}, {3, EdgeOpen}, {4, EdgeNone}, {7, EdgeClose}, {8, EdgeNone}} {
		if got := sch.Poll(D(c.h, 0, -1)); got != c.want {
			t.Errorf("%d:00 -> %q want %q", c.h, got, c.want)
		}
	}
}

func TestCancelOpenRefiresNextPoll(t *testing.T) {
	sch := NewOffPeakScheduler(weekly("02:00", "06:00", all()))
	if sch.Poll(D(3, 0, -1)) != EdgeOpen {
		t.Fatal("no open")
	}
	sch.CancelOpen()
	if sch.Poll(D(3, 30, -1)) != EdgeOpen {
		t.Error("did not re-fire")
	}
}

func TestSchedulerAutostartOnLaunchInsideWindow(t *testing.T) {
	sch := NewOffPeakScheduler(weekly("02:00", "06:00", all()))
	if sch.Poll(D(3, 0, -1)) != EdgeOpen {
		t.Error("no open on launch")
	}
}

func TestSummaryMetrics(t *testing.T) {
	s := NewOffPeakSession(time.Date(2026, 8, 20, 2, 0, 0, 0, time.Local))
	s.Snapshot = map[string]int64{"a": 0, "b": 1_000_000}
	s.CompletedBefore = map[string]struct{}{"b": {}}
	s.SpeedSamples = []float64{10, 20, 30}
	sum := Summarize(s, time.Date(2026, 8, 20, 4, 0, 0, 0, time.Local),
		map[string]int64{"a": 500_000_000, "b": 1_500_000_000}, []string{"a", "b"})
	if sum.BytesDownloaded != 500_000_000+1_499_000_000 {
		t.Errorf("bytes %d", sum.BytesDownloaded)
	}
	if sum.FilesCompleted != 1 || sum.DurationSeconds != 7200 {
		t.Errorf("%+v", sum)
	}
	if math.Abs(sum.PeakSpeedMbps-30) > 1e-9 {
		t.Errorf("peak %v", sum.PeakSpeedMbps)
	}
	want := float64(sum.BytesDownloaded) / (1024 * 1024) / 7200
	if math.Abs(sum.AvgSpeedMbps-want) > 1e-6 {
		t.Errorf("avg %v", sum.AvgSpeedMbps)
	}
	if sum.StartedAt != "2026-08-20T02:00:00" {
		t.Errorf("started_at %q", sum.StartedAt)
	}
}

func TestDescribe(t *testing.T) {
	dis := weekly("02:00", "06:00", all())
	dis.Enabled = false
	cases := []struct {
		s    Schedule
		want string
	}{
		{dis, ""},
		{weekly("02:00", "06:00", all()), "Daily 2:00 AM–6:00 AM"},
		{weekly("23:00", "05:00", days(0, 2, 4)), "Mon, Wed, Fri 11:00 PM–5:00 AM"},
		{Schedule{Enabled: true, Start: "02:00", End: "06:00", Recurrence: "once", Date: "2026-08-25"},
			"Once on 2026-08-25, 2:00 AM–6:00 AM"},
	}
	for _, c := range cases {
		if got := Describe(c.s); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestDescribeTargetsSuffix(t *testing.T) {
	s := weekly("02:00", "06:00", all())
	tg := []string{"a", "b", "c"}
	s.Targets = &tg
	if got := Describe(s); got != "Daily 2:00 AM–6:00 AM (3 downloads)" {
		t.Errorf("%q", got)
	}
	s.Targets = nil
	if got := Describe(s); got != "Daily 2:00 AM–6:00 AM" {
		t.Errorf("%q", got)
	}
	empty := []string{}
	s.Targets = &empty
	if got := Describe(s); got != "Daily 2:00 AM–6:00 AM" {
		t.Errorf("%q", got)
	}
}

func TestRegisterWakeCommand(t *testing.T) {
	today := time.Date(2026, 8, 20, 0, 0, 0, 0, time.Local)
	cmd := RegisterWakeCommand(weekly("02:00", "06:00", days(4, 0, 4)), `C:\It's\SilverSpoon.exe`, "--background", today)
	for _, want := range []string{
		`-Execute 'C:\It''s\SilverSpoon.exe' -Argument '--background'`,
		"New-ScheduledTaskTrigger -Weekly -DaysOfWeek Monday,Friday -At 02:00",
		"Register-ScheduledTask -TaskName 'SilverSpoonOffPeak'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("missing %q in %q", want, cmd)
		}
	}
	once := Schedule{Enabled: true, Start: "02:00", Recurrence: "once"}
	if got := TriggerExpr(once, today); got != "New-ScheduledTaskTrigger -Once -At '2026-08-20T02:00:00'" {
		t.Errorf("%q", got)
	}
	if got := TriggerExpr(weekly("02:00", "06:00", days()), today); got != "New-ScheduledTaskTrigger -Daily -At 02:00" {
		t.Errorf("%q", got)
	}
}
