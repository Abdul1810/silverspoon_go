package engine

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"silverspoon/internal/segmented"
	"silverspoon/internal/solver"
	"silverspoon/internal/store"
)

// Version is sent in the User-Agent of direct downloads and compared by the
// updater.
var Version = "v2.0.0"

// Log receives the engine's error lines.
var Log = slog.Default()

// Meter, when set, counts traffic and connections per network.
var Meter segmented.Meter

// Config is the part of the settings the engine reads; the app supplies a
// fresh copy on every call so settings changes apply immediately.
type Config struct {
	MaxWorkers     int
	MaxConnections int
	BindAddresses  string
	// Sources, when set, returns the local IPs to spread connections over
	// right now (all connected networks); it overrides BindAddresses.
	Sources        func() []string
	BandwidthLimit int // MB/s, 0 = unlimited
	AutoRetry      bool
}

// Engine owns the task list and every goroutine working on it.
type Engine struct {
	mu    sync.Mutex
	tasks []*Task

	Cfg         func() Config
	Solver      *solver.Solver
	AutoExtract atomic.Bool

	extracted map[string]bool // folders auto-extract already handled

	saveMu    sync.Mutex
	saveTimer *time.Timer

	off offpeakState

	stop chan struct{}
}

// New returns an engine over the persisted tasks.
func New(records []store.TaskRecord, cfg func() Config, s *solver.Solver) *Engine {
	e := &Engine{Cfg: cfg, Solver: s, extracted: map[string]bool{}, stop: make(chan struct{})}
	for _, r := range records {
		t := TaskFromRecord(r)
		// A folder interrupted mid-extraction counts as downloaded.
		if t.Status == StExtracting {
			t.Status = StCompleted
		}
		e.tasks = append(e.tasks, t)
	}
	return e
}

// Start runs the download manager.
func (e *Engine) Start() { go e.manager() }

// Close stops the manager.
func (e *Engine) Close() { close(e.stop) }

func (e *Engine) cfg() Config {
	c := Config{MaxWorkers: 3, MaxConnections: 4}
	if e.Cfg != nil {
		c = e.Cfg()
	}
	if c.MaxWorkers < 1 {
		c.MaxWorkers = 1
	}
	return c
}

// Views returns a copy of every task, in queue order.
func (e *Engine) Views() []View {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]View, len(e.tasks))
	for i, t := range e.tasks {
		out[i] = t.view()
	}
	return out
}

// View returns one task by uid.
func (e *Engine) View(uid string) (View, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t := e.find(uid); t != nil {
		return t.view(), true
	}
	return View{}, false
}

func (e *Engine) find(uid string) *Task {
	for _, t := range e.tasks {
		if t.UID == uid {
			return t
		}
	}
	return nil
}

func (e *Engine) pick(uids []string) []*Task {
	want := map[string]bool{}
	for _, u := range uids {
		want[u] = true
	}
	var out []*Task
	for _, t := range e.tasks {
		if want[t.UID] {
			out = append(out, t)
		}
	}
	return out
}

// RequestSave schedules a history save in 500 ms (debounced).
func (e *Engine) RequestSave() {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	if e.saveTimer != nil {
		e.saveTimer.Stop()
	}
	e.saveTimer = time.AfterFunc(500*time.Millisecond, func() { e.SaveNow() })
}

// SaveNow writes the history file.
func (e *Engine) SaveNow() {
	e.mu.Lock()
	recs := make([]store.TaskRecord, len(e.tasks))
	for i, t := range e.tasks {
		recs[i] = t.Record()
	}
	e.mu.Unlock()
	if err := store.SaveHistory(recs); err != nil {
		Log.Error("Failed to save history: " + err.Error())
	}
}

// manager starts Pending tasks up to MaxWorkers. CAPTCHA resolution belongs
// to the same worker slot as the transfer, so it counts as active.
func (e *Engine) manager() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		max := e.cfg().MaxWorkers
		e.mu.Lock()
		active := 0
		for _, t := range e.tasks {
			switch t.Status {
			case StDownloading, StStarting, StSolving, StWaitingUser:
				active++
			}
		}
		if active < max {
			for _, t := range e.tasks {
				if t.Status == StPending {
					t.Status = StStarting
					go e.worker(t)
					active++
					if active >= max {
						break
					}
				}
			}
		}
		e.mu.Unlock()
		if e.AutoExtract.Load() {
			e.checkExtraction()
		}
		select {
		case <-e.stop:
			return
		case <-tick.C:
		}
	}
}

// AddLinks queues links into folder under saveDir and returns their uids.
func (e *Engine) AddLinks(links []string, saveDir, folder string) []string {
	e.mu.Lock()
	var uids []string
	for _, l := range links {
		t := NewTask(l, saveDir, folder)
		e.tasks = append(e.tasks, t)
		uids = append(uids, t.UID)
	}
	e.mu.Unlock()
	e.RequestSave()
	return uids
}

// AddUpdateTask queues an app-update download.
func (e *Engine) AddUpdateTask(url, saveDir, folder, version string) string {
	t := NewTask(url, saveDir, folder)
	t.IsUpdate = true
	v := version
	t.UpdateVersion = &v
	e.mu.Lock()
	e.tasks = append(e.tasks, t)
	e.mu.Unlock()
	e.RequestSave()
	return t.UID
}

// ParseLinks pulls http(s) lines out of pasted text ("- " bullets allowed).
func ParseLinks(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		s = strings.TrimLeft(s, "- ")
		if s != "" && strings.HasPrefix(s, "http") {
			out = append(out, s)
		}
	}
	return out
}

// StartTasks sets resumable tasks to Pending.
func (e *Engine) StartTasks(uids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.pick(uids) {
		switch t.Status {
		case StQueued, StCancelled, StError, StPaused, StPausing, StCaptchaTimeout, StSolving, StWaitingUser:
			t.Status, t.ErrorMessage, t.CancelFlag, t.PauseFlag = StPending, "", false, false
		}
	}
}

// PauseTasks pauses active tasks.
func (e *Engine) PauseTasks(uids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.pick(uids) {
		pauseTask(t)
	}
}

func pauseTask(t *Task) {
	switch t.Status {
	case StDownloading, StPending, StStarting:
		t.PauseFlag = true
		if t.Status == StDownloading {
			t.Status = StPausing
		} else {
			t.Status = StPaused
		}
	}
}

// CancelTasks cancels downloads, or stops an extraction in progress.
func (e *Engine) CancelTasks(uids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.pick(uids) {
		switch t.Status {
		case StExtracting:
			// extractFolder notices within ~0.2 s, kills the extractor and sets
			// "Extract Cancelled" (the download itself is kept).
			t.CancelFlag = true
		case StDownloading, StPending, StPaused, StStarting, StQueued:
			t.CancelFlag, t.PauseFlag, t.Status = true, false, StCancelled
		}
	}
}

// RetryTasks re-queues failed tasks.
func (e *Engine) RetryTasks(uids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.pick(uids) {
		if strings.Contains(t.Status, "Error") || t.Status == StCaptchaTimeout {
			t.Status, t.ErrorMessage, t.CancelFlag, t.PauseFlag, t.RetryCount = StPending, "", false, false, 0
		}
	}
}

var activeForRedownload = map[string]bool{StDownloading: true, StPending: true, StStarting: true,
	StPausing: true, StExtracting: true}

// NeedsRedownloadConfirm counts selected tasks whose progress a forced
// redownload would throw away.
func (e *Engine) NeedsRedownloadConfirm(uids []string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, t := range e.pick(uids) {
		if !activeForRedownload[t.Status] && t.Progress > 0 && t.Status != StError {
			n++
		}
	}
	return n
}

// ForceRedownload deletes the files of the selected tasks and queues them
// from 0%. It returns how many were queued, skipped (active) and failed.
func (e *Engine) ForceRedownload(uids []string) (queued, skipped, failed int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.pick(uids) {
		if activeForRedownload[t.Status] {
			skipped++
			continue
		}
		if err := removeIfExists(t.FilePath); err == nil {
			err = removeIfExists(segmented.StatePath(t.FilePath))
			if err != nil {
				failed++
				t.Status = StError
				t.ErrorMessage = "Could not delete existing file before redownload. " + FormatError(err)
				continue
			}
		} else {
			failed++
			t.Status = StError
			t.ErrorMessage = "Could not delete existing file before redownload. " + FormatError(err)
			continue
		}
		t.CancelFlag, t.PauseFlag = false, false
		t.Progress, t.Speed, t.DownloadedBytes, t.TotalBytes = 0, 0, 0, 0
		t.ErrorMessage, t.Status = "", StPending
		// A fresh download of an update asks again when it completes.
		t.InstallHandled = false
		delete(e.extracted, t.FolderName)
		queued++
	}
	return
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeleteTasks cancels and removes tasks, optionally deleting their files.
func (e *Engine) DeleteTasks(uids []string, deleteFiles bool) {
	e.mu.Lock()
	targets := e.pick(uids)
	gone := map[*Task]bool{}
	for _, t := range targets {
		t.CancelFlag, t.Status = true, StCancelled
		if deleteFiles {
			if _, err := os.Stat(t.FilePath); err == nil {
				if err := os.Remove(t.FilePath); err != nil {
					Log.Error("Failed to delete " + t.FilePath + ": " + err.Error())
				} else {
					removeIfExists(segmented.StatePath(t.FilePath))
				}
			}
		}
		gone[t] = true
	}
	e.tasks = filterTasks(e.tasks, func(t *Task) bool { return !gone[t] })
	e.mu.Unlock()
	e.RequestSave()
}

// ClearFinished removes Completed/Extracted/Cancelled tasks.
func (e *Engine) ClearFinished() bool {
	e.mu.Lock()
	n := len(e.tasks)
	e.tasks = filterTasks(e.tasks, func(t *Task) bool {
		switch t.Status {
		case StCompleted, StExtracted, StCancelled, StExtractCanceled:
			return false
		}
		return true
	})
	changed := len(e.tasks) != n
	e.mu.Unlock()
	if changed {
		e.RequestSave()
	}
	return changed
}

// SetInstallHandled marks an update task's install prompt as answered.
func (e *Engine) SetInstallHandled(uid string) {
	e.mu.Lock()
	if t := e.find(uid); t != nil {
		t.InstallHandled = true
	}
	e.mu.Unlock()
	e.RequestSave()
}

// PauseAllForClose pauses running downloads and gives their workers up to
// timeout to flush the saved segment progress, so a restart resumes exactly
// where they were; then it saves the history.
func (e *Engine) PauseAllForClose(timeout time.Duration) {
	e.mu.Lock()
	var active []*Task
	for _, t := range e.tasks {
		if t.Status == StDownloading {
			t.PauseFlag = true
			active = append(active, t)
		}
	}
	e.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for len(active) > 0 && time.Now().Before(deadline) {
		e.mu.Lock()
		busy := false
		for _, t := range active {
			if t.Status == StDownloading {
				busy = true
			}
		}
		e.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.SaveNow()
}

func filterTasks(ts []*Task, keep func(*Task) bool) []*Task {
	out := ts[:0]
	for _, t := range ts {
		if keep(t) {
			out = append(out, t)
		}
	}
	for i := len(out); i < len(ts); i++ {
		ts[i] = nil
	}
	return out
}

// update runs f with the engine lock held.
func (e *Engine) update(f func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f()
}

func (e *Engine) flags(t *Task) (pause, cancel bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return t.PauseFlag, t.CancelFlag
}
