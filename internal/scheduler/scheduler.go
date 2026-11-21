// Package scheduler is the off-peak download scheduler (port of scheduler.py).
//
// Pure logic plus thin OS helpers. The GUI owns the timer and the task list;
// this package only decides when a window opens/closes and computes the session
// summary. Windows-only bits (wake timer) degrade to no-ops elsewhere.
package scheduler

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WeekdayNames is indexed Mon=0 .. Sun=6 (Python's datetime.weekday()).
var WeekdayNames = []string{"Monday", "Tuesday", "Wednesday", "Thursday",
	"Friday", "Saturday", "Sunday"}

// WakeTaskName is the Windows scheduled task that wakes the PC.
const WakeTaskName = "SilverSpoonOffPeak"

// Schedule mirrors the "schedule" object in ~/.silverspoon_settings.json.
//
// Days is a pointer so that "key absent" (nil: every day) stays distinct from
// "present but empty" (never active). Targets is nil for the whole queue, or
// the uids of the tasks the window should start.
type Schedule struct {
	Enabled    bool      `json:"enabled"`
	Start      string    `json:"start"`
	End        string    `json:"end"`
	Recurrence string    `json:"recurrence"`
	Days       *[]int    `json:"days,omitempty"`
	Date       string    `json:"date"`
	WakeTimer  bool      `json:"wake_timer"`
	KeepAwake  bool      `json:"keep_awake"`
	Targets    *[]string `json:"targets,omitempty"`
}

// DefaultSchedule is the additive default; an absent "schedule" key means the
// feature is off.
func DefaultSchedule() Schedule {
	days := []int{0, 1, 2, 3, 4, 5, 6}
	return Schedule{
		Start: "02:00", End: "06:00", Recurrence: "weekly",
		Days: &days, KeepAwake: true,
	}
}

// ParseHHMM parses "HH:mm" into hour and minute.
func ParseHHMM(v string) (int, int, error) {
	parts := strings.Split(v, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad time %q", v)
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("bad time %q", v)
	}
	return h, m, nil
}

// Split12h converts "HH:mm" (24h) to (hour 1-12, minute, "AM"|"PM").
// 00:xx -> 12 AM, 12:xx -> 12 PM.
func Split12h(hhmm string) (int, int, string) {
	h, m, _ := ParseHHMM(hhmm)
	ampm := "AM"
	if h >= 12 {
		ampm = "PM"
	}
	h12 := h % 12
	if h12 == 0 {
		h12 = 12
	}
	return h12, m, ampm
}

// Join24h converts (hour 1-12, minute, "AM"|"PM") to "HH:mm" (24h).
func Join24h(hour12, minute int, ampm string) string {
	h := hour12 % 12
	if ampm == "PM" {
		h += 12
	}
	return fmt.Sprintf("%02d:%02d", h, minute)
}

func fmtTime(hhmm string) string {
	h, m, ampm := Split12h(hhmm)
	return fmt.Sprintf("%d:%02d %s", h, m, ampm)
}

func (s Schedule) days() []int {
	if s.Days == nil {
		return []int{0, 1, 2, 3, 4, 5, 6}
	}
	return *s.Days
}

func uniqueSorted(days []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, d := range days {
		if d >= 0 && d < 7 && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Ints(out)
	return out
}

// Describe is a one-line summary of an armed schedule; "" when not enabled.
// Examples: "Daily 2:00 AM–6:00 AM", "Mon, Wed, Fri 11:00 PM–5:00 AM",
// "Once on 2026-08-25, 2:00 AM–6:00 AM", with " (N downloads)" appended when
// the schedule targets a specific non-empty list of downloads.
func Describe(s Schedule) string {
	if !s.Enabled {
		return ""
	}
	window := fmtTime(s.Start) + "–" + fmtTime(s.End)
	var prefix string
	if s.Recurrence == "once" {
		prefix = "Once on " + s.Date + ", "
	} else {
		days := uniqueSorted(s.days())
		if len(days) == 7 {
			prefix = "Daily "
		} else {
			names := make([]string, len(days))
			for i, d := range days {
				names[i] = WeekdayNames[d][:3]
			}
			prefix = strings.Join(names, ", ") + " "
		}
	}
	text := prefix + window
	if s.Targets != nil && len(*s.Targets) > 0 {
		text += fmt.Sprintf(" (%d downloads)", len(*s.Targets))
	}
	return text
}

// Weekday returns Mon=0 .. Sun=6.
func Weekday(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

func dayActive(startDate time.Time, s Schedule) bool {
	if s.Recurrence == "once" {
		return startDate.Format("2006-01-02") == s.Date
	}
	wd := Weekday(startDate)
	for _, d := range s.days() {
		if d == wd {
			return true
		}
	}
	return false
}

// IsWithinWindow reports whether now falls inside an active off-peak window.
//
// Windows that cross midnight are handled by testing both the window that
// could have started today and the one that started yesterday. Weekday/once
// rules use the window's start date. start == end is a full 24h window.
func IsWithinWindow(now time.Time, s Schedule) bool {
	if !s.Enabled {
		return false
	}
	sh, sm, err := ParseHHMM(s.Start)
	if err != nil {
		return false
	}
	eh, em, err := ParseHHMM(s.End)
	if err != nil {
		return false
	}
	crosses := eh*60+em <= sh*60+sm
	for off := 0; off <= 1; off++ {
		d := now.AddDate(0, 0, -off)
		startDT := time.Date(d.Year(), d.Month(), d.Day(), sh, sm, 0, 0, now.Location())
		endDT := time.Date(d.Year(), d.Month(), d.Day(), eh, em, 0, 0, now.Location())
		if crosses {
			endDT = endDT.AddDate(0, 0, 1)
		}
		if !now.Before(startDT) && now.Before(endDT) && dayActive(startDT, s) {
			return true
		}
	}
	return false
}

// Edge is what OffPeakScheduler.Poll reports.
type Edge string

const (
	EdgeNone  Edge = ""
	EdgeOpen  Edge = "open"
	EdgeClose Edge = "close"
)

// OffPeakScheduler is an edge detector over IsWithinWindow. It starts
// inactive, so launching the app inside a window fires one "open".
type OffPeakScheduler struct {
	Sched  Schedule
	active bool
}

// NewOffPeakScheduler returns an inactive scheduler for s.
func NewOffPeakScheduler(s Schedule) *OffPeakScheduler { return &OffPeakScheduler{Sched: s} }

// Update replaces the schedule.
func (o *OffPeakScheduler) Update(s Schedule) { o.Sched = s }

// CancelOpen undoes an "open" edge that couldn't be honoured (e.g. no
// connection), so the next poll inside the window re-fires "open".
func (o *OffPeakScheduler) CancelOpen() { o.active = false }

// Poll returns EdgeOpen/EdgeClose on transitions, EdgeNone otherwise.
func (o *OffPeakScheduler) Poll(now time.Time) Edge {
	inside := IsWithinWindow(now, o.Sched)
	if inside && !o.active {
		o.active = true
		return EdgeOpen
	}
	if !inside && o.active {
		o.active = false
		return EdgeClose
	}
	return EdgeNone
}

// OffPeakSession accounts for one open window.
type OffPeakSession struct {
	StartedAt       time.Time
	Snapshot        map[string]int64    // task key -> downloaded bytes at open
	CompletedBefore map[string]struct{} // task keys already done at open
	SpeedSamples    []float64           // global MB/s samples across the window
}

// NewOffPeakSession starts accounting at startedAt.
func NewOffPeakSession(startedAt time.Time) *OffPeakSession {
	return &OffPeakSession{StartedAt: startedAt, Snapshot: map[string]int64{},
		CompletedBefore: map[string]struct{}{}}
}

// SampleSpeed records a positive MB/s sample.
func (s *OffPeakSession) SampleSpeed(mbps float64) {
	if mbps > 0 {
		s.SpeedSamples = append(s.SpeedSamples, mbps)
	}
}

// Summary is one line of ~/.silverspoon_offpeak_report.jsonl.
type Summary struct {
	StartedAt       string  `json:"started_at"`
	EndedAt         string  `json:"ended_at"`
	DurationSeconds float64 `json:"duration_seconds"`
	BytesDownloaded int64   `json:"bytes_downloaded"`
	FilesCompleted  int     `json:"files_completed"`
	AvgSpeedMbps    float64 `json:"avg_speed_mbps"`
	PeakSpeedMbps   float64 `json:"peak_speed_mbps"`
}

const isoSeconds = "2006-01-02T15:04:05"

// Summarize computes the window's metrics from open->close deltas.
func Summarize(s *OffPeakSession, endAt time.Time, bytesNow map[string]int64, completedNow []string) Summary {
	var total int64
	for k, nb := range bytesNow {
		if d := nb - s.Snapshot[k]; d > 0 {
			total += d
		}
	}
	done := map[string]struct{}{}
	for _, k := range completedNow {
		if _, before := s.CompletedBefore[k]; !before {
			done[k] = struct{}{}
		}
	}
	dur := endAt.Sub(s.StartedAt).Seconds()
	if dur < 0 {
		dur = 0
	}
	peak := 0.0
	for _, v := range s.SpeedSamples {
		if v > peak {
			peak = v
		}
	}
	avg := 0.0
	if dur > 0 {
		avg = float64(total) / (1024 * 1024) / dur
	}
	return Summary{
		StartedAt: s.StartedAt.Format(isoSeconds), EndedAt: endAt.Format(isoSeconds),
		DurationSeconds: dur, BytesDownloaded: total, FilesCompleted: len(done),
		AvgSpeedMbps: avg, PeakSpeedMbps: peak,
	}
}

// AppendReport appends one JSON line; best effort.
func AppendReport(sum Summary, path string) bool {
	b, err := json.Marshal(sum)
	if err != nil {
		return false
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err == nil
}

// ReportPath is ~/.silverspoon_offpeak_report.jsonl.
func ReportPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".silverspoon_offpeak_report.jsonl")
}

// CheckConnection dials 1.1.1.1:443 with a 1.5 s timeout. Called from a
// short-lived goroutine so it never blocks the GUI.
func CheckConnection() bool {
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", 1500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// TriggerExpr is the New-ScheduledTaskTrigger expression for the schedule.
func TriggerExpr(s Schedule, today time.Time) string {
	at := s.Start
	if s.Recurrence == "once" {
		date := s.Date
		if date == "" {
			date = today.Format("2006-01-02")
		}
		return fmt.Sprintf("New-ScheduledTaskTrigger -Once -At '%sT%s:00'", date, at)
	}
	days := s.days()
	if len(days) == 0 {
		days = []int{0, 1, 2, 3, 4, 5, 6}
	}
	if len(days) == 7 {
		return "New-ScheduledTaskTrigger -Daily -At " + at
	}
	u := uniqueSorted(days)
	names := make([]string, len(u))
	for i, d := range u {
		names[i] = WeekdayNames[d]
	}
	return fmt.Sprintf("New-ScheduledTaskTrigger -Weekly -DaysOfWeek %s -At %s", strings.Join(names, ","), at)
}

func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

// RegisterWakeCommand is the PowerShell script that registers a per-user task
// which wakes the PC and launches the app at the window start.
func RegisterWakeCommand(s Schedule, executable, arguments string, today time.Time) string {
	argPart := ""
	if arguments != "" {
		argPart = " -Argument '" + psQuote(arguments) + "'"
	}
	return fmt.Sprintf("$a = New-ScheduledTaskAction -Execute '%s'%s; "+
		"$t = %s; "+
		"$s = New-ScheduledTaskSettingsSet -WakeToRun -AllowStartIfOnBatteries "+
		"-DontStopIfGoingOnBatteries; "+
		"Register-ScheduledTask -TaskName '%s' -Action $a "+
		"-Trigger $t -Settings $s -Force | Out-Null",
		psQuote(executable), argPart, TriggerExpr(s, today), WakeTaskName)
}

// UnregisterWakeCommand is the PowerShell script that removes the wake task.
func UnregisterWakeCommand() string {
	return fmt.Sprintf("Unregister-ScheduledTask -TaskName '%s' "+
		"-Confirm:$false -ErrorAction SilentlyContinue", WakeTaskName)
}
