package engine

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"silverspoon/internal/scheduler"
	"silverspoon/internal/segmented"
	"silverspoon/internal/solver"
	"silverspoon/internal/store"
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// fileServer serves files by path with Range support, slowly when rate > 0.
func fileServer(files map[string][]byte, rate int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/named") {
			w.Header().Set("Content-Disposition", `attachment; filename="Server Name.bin"`)
		}
		if rate > 0 {
			w = &slowWriter{ResponseWriter: w, rate: rate}
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
}

type slowWriter struct {
	http.ResponseWriter
	rate int
}

func (s *slowWriter) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		c := min(len(p), 32*1024)
		m, err := s.ResponseWriter.Write(p[:c])
		n += m
		if err != nil {
			return n, err
		}
		time.Sleep(time.Duration(float64(c) / float64(s.rate) * float64(time.Second)))
		p = p[c:]
	}
	return n, nil
}

func newEngine(t *testing.T, cfg Config) *Engine {
	old := store.Dir
	store.Dir = t.TempDir()
	t.Cleanup(func() { store.Dir = old })
	e := New(nil, func() Config { return cfg }, nil)
	e.Start()
	t.Cleanup(e.Close)
	return e
}

func waitStatus(t *testing.T, e *Engine, uid string, want ...string) View {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := e.View(uid)
		for _, w := range want {
			if v.Status == w {
				return v
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	v, _ := e.View(uid)
	t.Fatalf("status %q (%s), want %v", v.Status, v.ErrorMessage, want)
	return v
}

func TestDirectSmallAndSegmentedDownloads(t *testing.T) {
	small, big := randBytes(300_000), randBytes(20*1024*1024)
	srv := fileServer(map[string][]byte{"/small.zip": small, "/big.rar": big}, 0)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 3, MaxConnections: 4})
	dir := t.TempDir()
	uids := e.AddLinks([]string{srv.URL + "/small.zip", srv.URL + "/big.rar"}, dir, "Batch")
	e.StartTasks(uids)
	for i, want := range [][]byte{small, big} {
		v := waitStatus(t, e, uids[i], StCompleted)
		got, _ := os.ReadFile(v.FilePath)
		if !bytes.Equal(got, want) || v.Progress != 100 || v.FilePath != filepath.Join(dir, "Batch", filepath.Base(v.FilePath)) {
			t.Errorf("task %d: equal=%v progress=%v path=%s", i, bytes.Equal(got, want), v.Progress, v.FilePath)
		}
	}
	e.SaveNow()
	recs := store.LoadHistory()
	if len(recs) != 2 || recs[0].Status != StCompleted || recs[1].TotalBytes != int64(len(big)) {
		t.Errorf("history %+v", recs)
	}
}

func TestPauseResumeAndCloseRestoresPaused(t *testing.T) {
	data := randBytes(16 * 1024 * 1024)
	srv := fileServer(map[string][]byte{"/f.rar": data}, 2*1024*1024)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 1, MaxConnections: 4})
	uid := e.AddLinks([]string{srv.URL + "/f.rar"}, t.TempDir(), "F")[0]
	e.StartTasks([]string{uid})
	waitStatus(t, e, uid, StDownloading)
	time.Sleep(1500 * time.Millisecond)
	e.PauseTasks([]string{uid})
	v := waitStatus(t, e, uid, StPaused)
	if v.DownloadedBytes <= 0 || v.DownloadedBytes >= int64(len(data)) {
		t.Fatalf("paused at %d", v.DownloadedBytes)
	}
	if _, err := os.Stat(segmented.StatePath(v.FilePath)); err != nil {
		t.Fatal("no segment state after pause")
	}
	// A task saved while downloading comes back Paused.
	r := store.TaskRecord{UID: "x", Link: srv.URL + "/f.rar", BaseSaveDir: "d", FolderName: "F", Status: StDownloading}
	if TaskFromRecord(r).Status != StPaused {
		t.Error("in-flight status not restored as Paused")
	}
	e.StartTasks([]string{uid})
	v = waitStatus(t, e, uid, StCompleted)
	got, _ := os.ReadFile(v.FilePath)
	if !bytes.Equal(got, data) {
		t.Error("content differs after resume")
	}
}

func TestErrorsRetryAndCancel(t *testing.T) {
	srv := fileServer(map[string][]byte{}, 0)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 2, MaxConnections: 4})
	uid := e.AddLinks([]string{srv.URL + "/missing.zip"}, t.TempDir(), "M")[0]
	e.StartTasks([]string{uid})
	v := waitStatus(t, e, uid, StError)
	if !strings.HasPrefix(v.ErrorMessage, "Download failed. HTTPError: HTTP Error 404") {
		t.Errorf("message %q", v.ErrorMessage)
	}
	e.RetryTasks([]string{uid})
	if v, _ := e.View(uid); v.Status != StPending && v.Status != StStarting {
		t.Errorf("retry -> %q", v.Status)
	}
	waitStatus(t, e, uid, StError)
	e.CancelTasks([]string{uid}) // Error isn't cancellable
	if v, _ := e.View(uid); v.Status != StError {
		t.Errorf("cancel changed an errored task to %q", v.Status)
	}
	q := e.AddLinks([]string{srv.URL + "/x.zip"}, t.TempDir(), "Q")[0]
	e.CancelTasks([]string{q})
	if v, _ := e.View(q); v.Status != StCancelled {
		t.Errorf("queued cancel -> %q", v.Status)
	}
	e.ClearFinished()
	if _, ok := e.View(q); ok {
		t.Error("cancelled task not cleared")
	}
	e.DeleteTasks([]string{uid}, false)
	if len(e.Views()) != 0 {
		t.Error("delete failed")
	}
}

func TestServerFilenameAdopted(t *testing.T) {
	srv := fileServer(map[string][]byte{"/named/abc123": randBytes(1000)}, 0)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 1, MaxConnections: 4})
	uid := e.AddLinks([]string{srv.URL + "/named/abc123"}, t.TempDir(), "N")[0]
	e.StartTasks([]string{uid})
	v := waitStatus(t, e, uid, StCompleted)
	if v.Filename != "Server Name.bin" || filepath.Base(v.FilePath) != "Server Name.bin" {
		t.Errorf("filename %q path %q", v.Filename, v.FilePath)
	}
}

func TestForceRedownload(t *testing.T) {
	data := randBytes(5000)
	srv := fileServer(map[string][]byte{"/a.zip": data}, 0)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 1, MaxConnections: 4})
	uid := e.AddLinks([]string{srv.URL + "/a.zip"}, t.TempDir(), "A")[0]
	e.StartTasks([]string{uid})
	waitStatus(t, e, uid, StCompleted)
	if e.NeedsRedownloadConfirm([]string{uid}) != 1 {
		t.Error("completed task should need confirmation")
	}
	q, s, f := e.ForceRedownload([]string{uid})
	if q != 1 || s != 0 || f != 0 {
		t.Errorf("%d %d %d", q, s, f)
	}
	waitStatus(t, e, uid, StCompleted)
}

func sevenZipPath() string {
	if runtime.GOOS == "windows" {
		p, _ := filepath.Abs(filepath.Join("..", "..", "assets", "7z.exe"))
		if exists(p) {
			return p
		}
	}
	return which("7z", "7zz", "7za")
}

func TestAutoExtract(t *testing.T) {
	sz := sevenZipPath()
	if sz == "" {
		t.Skip("no 7z")
	}
	solver.AppDir, _ = filepath.Abs(filepath.Join("..", ".."))
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "game.bin"), randBytes(2_000_000), 0o644)
	arc := filepath.Join(t.TempDir(), "Game.part1.rar")
	// 7-Zip can't write RAR; a zip named like a standalone archive works.
	arc = strings.TrimSuffix(arc, ".part1.rar") + ".zip"
	if b, err := exec.Command(sz, "a", "-tzip", "-mx0", arc, filepath.Join(src, "game.bin")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	zipData, _ := os.ReadFile(arc)
	srv := fileServer(map[string][]byte{"/Game.zip": zipData}, 0)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 1, MaxConnections: 4})
	e.AutoExtract.Store(true)
	dir := t.TempDir()
	uid := e.AddLinks([]string{srv.URL + "/Game.zip"}, dir, "Game")[0]
	e.StartTasks([]string{uid})
	waitStatus(t, e, uid, StExtracted)
	if st, err := os.Stat(filepath.Join(dir, "Game", "game.bin")); err != nil || st.Size() != 2_000_000 {
		t.Errorf("extracted file: %v", err)
	}
	if !e.AlreadyExtracted("Game") {
		t.Error("AlreadyExtracted false")
	}
}

func TestOffpeakWindowStartsAndPausesTasks(t *testing.T) {
	data := randBytes(16 * 1024 * 1024)
	srv := fileServer(map[string][]byte{"/w.rar": data}, 1024*1024)
	defer srv.Close()
	e := newEngine(t, Config{MaxWorkers: 1, MaxConnections: 2})
	scheduler.ReportFile = filepath.Join(t.TempDir(), "report.jsonl")
	defer func() { scheduler.ReportFile = "" }()
	uid := e.AddLinks([]string{srv.URL + "/w.rar"}, t.TempDir(), "W")[0]
	now := time.Now()
	days := []int{0, 1, 2, 3, 4, 5, 6}
	start := now.Add(-time.Minute).Format("15:04")
	end := now.Add(10 * time.Minute).Format("15:04")
	e.SetSchedule(scheduler.Schedule{Enabled: true, Start: start, End: end, Recurrence: "weekly", Days: &days})
	e.SchedulerTick(now, nil)
	waitStatus(t, e, uid, StDownloading)
	if !e.WindowActive() {
		t.Error("window not active")
	}
	time.Sleep(1200 * time.Millisecond)
	var sum scheduler.Summary
	closed := false
	e.mu.Lock()
	e.off.ctl.Sched.End = now.Add(-30 * time.Second).Format("15:04")
	e.off.ctl.Sched.Start = now.Add(-2 * time.Minute).Format("15:04")
	e.mu.Unlock()
	e.SchedulerTick(now.Add(time.Second), func(s scheduler.Summary, _ string, _ bool) { sum, closed = s, true })
	waitStatus(t, e, uid, StPaused)
	if !closed || sum.BytesDownloaded <= 0 {
		t.Errorf("closed=%v summary %+v", closed, sum)
	}
	if b, err := os.ReadFile(scheduler.ReportFile); err != nil || !strings.Contains(string(b), "bytes_downloaded") {
		t.Errorf("report: %v %s", err, b)
	}
}
