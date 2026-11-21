package engine

import (
	"fmt"
	"sync/atomic"
	"time"

	"silverspoon/internal/platform"
	"silverspoon/internal/scheduler"
)

type offpeakState struct {
	ctl     *scheduler.OffPeakScheduler
	session *scheduler.OffPeakSession
	uids    map[string]bool // nil = whole queue
	probing atomic.Bool
}

// SetSchedule installs or replaces the off-peak schedule.
func (e *Engine) SetSchedule(s scheduler.Schedule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.off.ctl == nil {
		e.off.ctl = scheduler.NewOffPeakScheduler(s)
	} else {
		e.off.ctl.Update(s)
	}
}

// WindowActive reports whether an off-peak window is currently open.
func (e *Engine) WindowActive() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.off.session != nil
}

var doneStatuses = map[string]bool{StCompleted: true, StExtracted: true, StExtractCanceled: true}

// scheduledTasks are the tasks the schedule targets: all, or the chosen uids.
// Lock held.
func (e *Engine) scheduledTasks(s scheduler.Schedule) []*Task {
	if s.Targets == nil || len(*s.Targets) == 0 {
		return append([]*Task(nil), e.tasks...)
	}
	return e.pick(*s.Targets)
}

// SchedulerTick polls the window (every 30 s, and once 1.5 s after launch).
// When a window closes, onClose gets the summary, the report path and
// whether the report was written, so the UI can show it.
func (e *Engine) SchedulerTick(now time.Time, onClose func(scheduler.Summary, string, bool)) {
	e.mu.Lock()
	if e.off.ctl == nil {
		e.mu.Unlock()
		return
	}
	if e.off.session != nil {
		var speed float64
		for _, t := range e.tasks {
			if t.Status == StDownloading {
				speed += t.Speed
			}
		}
		e.off.session.SampleSpeed(speed)
	}
	edge := e.off.ctl.Poll(now)
	sched := e.off.ctl.Sched
	e.mu.Unlock()

	switch edge {
	case scheduler.EdgeOpen:
		// Probe connectivity off the UI goroutine; with no connection the next
		// tick (still inside the window) re-fires "open" and retries.
		if !e.off.probing.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer e.off.probing.Store(false)
			if !scheduler.CheckConnection() {
				e.mu.Lock()
				e.off.ctl.CancelOpen()
				e.mu.Unlock()
				Log.Warn("Off-peak: no internet connection at window open; will retry.")
				return
			}
			e.offpeakOpen(now, sched)
		}()
	case scheduler.EdgeClose:
		e.offpeakClose(now, onClose)
	}
}

func (e *Engine) offpeakOpen(now time.Time, sched scheduler.Schedule) {
	if sched.KeepAwake {
		platform.PreventSleep()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	scheduled := e.scheduledTasks(sched)
	sess := scheduler.NewOffPeakSession(now)
	uids := map[string]bool{}
	for _, t := range scheduled {
		sess.Snapshot[t.UID] = t.DownloadedBytes
		if doneStatuses[t.Status] {
			sess.CompletedBefore[t.UID] = struct{}{}
		}
		uids[t.UID] = true
	}
	e.off.session, e.off.uids = sess, uids
	started := 0
	for _, t := range scheduled {
		switch t.Status {
		case StQueued, StPaused, StCancelled, StError, StCaptchaTimeout:
			t.Status, t.ErrorMessage, t.CancelFlag, t.PauseFlag = StPending, "", false, false
			started++
		}
	}
	Log.Info(fmt.Sprintf("Download window opened; queued %d task(s).", started))
}

func (e *Engine) offpeakClose(now time.Time, onClose func(scheduler.Summary, string, bool)) {
	platform.AllowSleep()
	e.mu.Lock()
	uids := e.off.uids
	// Pause only the tasks this window was responsible for.
	for _, t := range e.tasks {
		if uids != nil && !uids[t.UID] {
			continue
		}
		pauseTask(t)
	}
	sess := e.off.session
	e.off.session = nil
	if sess == nil {
		e.mu.Unlock()
		return
	}
	bytesNow := map[string]int64{}
	var completed []string
	for _, t := range e.tasks {
		if uids != nil && !uids[t.UID] {
			continue
		}
		bytesNow[t.UID] = t.DownloadedBytes
		if doneStatuses[t.Status] {
			completed = append(completed, t.UID)
		}
	}
	e.mu.Unlock()
	sum := scheduler.Summarize(sess, now, bytesNow, completed)
	path := scheduler.ReportPath()
	saved := scheduler.AppendReport(sum, path)
	if !saved {
		Log.Warn("Failed to append off-peak report to " + path)
	}
	Log.Info(fmt.Sprintf("Scheduled download window closed; %+v", sum))
	if onClose != nil {
		onClose(sum, path, saved)
	}
}
