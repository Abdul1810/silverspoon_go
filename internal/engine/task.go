// Package engine is the download queue: tasks, the manager that starts them,
// the per-task workers, extraction and the off-peak window (the non-UI half of
// MainWindow in pyqt_downloader.py).
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"

	"silverspoon/internal/providers"
	"silverspoon/internal/store"
)

// Status strings are shared with the Python app through the history file, so
// they must stay exactly as they are.
const (
	StQueued          = "Queued"
	StPending         = "Pending"
	StStarting        = "Starting..."
	StDownloading     = "Downloading"
	StPausing         = "Pausing..."
	StPaused          = "Paused"
	StCancelled       = "Cancelled"
	StCompleted       = "Completed"
	StError           = "Error"
	StSolving         = "Solving CAPTCHA..."
	StWaitingUser     = "Waiting for you to solve CAPTCHA"
	StCaptchaTimeout  = "CAPTCHA Timeout"
	StResolving       = "Resolving Container..."
	StExtracting      = "Extracting..."
	StExtracted       = "Extracted"
	StExtractCanceled = "Extract Cancelled"
)

// Task is one download. Its fields are guarded by the owning Engine's mutex.
type Task struct {
	UID         string
	Link        string
	BaseSaveDir string
	FolderName  string
	Filename    string
	FileID      string
	SaveDir     string
	FilePath    string

	Status          string
	Progress        float64
	Speed           float64 // MB/s
	DownloadedBytes int64
	TotalBytes      int64
	ErrorMessage    string
	RetryCount      int
	PauseFlag       bool
	CancelFlag      bool

	IsUpdate       bool
	UpdateVersion  *string
	InstallHandled bool

	// ExtractProgress is 0-100 while Extracting..., or nil when the extractor
	// doesn't report one. Not persisted.
	ExtractProgress *float64

	dlCookies   map[string]string
	dlUserAgent string
}

var rarSuffix = regexp.MustCompile(`(?i)^(.*?)(\.part\d+\.rar|\.rar)$`)

// FolderFromFilename is the default batch folder for a file name.
func FolderFromFilename(name string) string {
	if m := rarSuffix.FindStringSubmatch(name); m != nil {
		return strings.Trim(m[1], "._-")
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[:i]
	}
	return name
}

// SuggestFolder is the batch-folder name offered when adding links.
func SuggestFolder(firstLink string) string {
	s := FolderFromFilename(providers.FilenameFromLink(firstLink))
	s = strings.ReplaceAll(s, "_--_fitgirl-repacks.site", "")
	s = strings.ReplaceAll(s, "_--_", " ")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.TrimSpace(s)
}

func newUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewTask creates a queued task. folder "" derives one from the file name.
func NewTask(link, baseSaveDir, folder string) *Task {
	link = strings.TrimSpace(link)
	t := &Task{Link: link, BaseSaveDir: baseSaveDir, UID: newUID(), Status: StQueued}
	t.Filename = providers.FilenameFromLink(link)
	parts := strings.Split(strings.TrimRight(link, "/"), "/")
	t.FileID = strings.Split(parts[len(parts)-1], "#")[0]
	if folder != "" {
		t.FolderName = folder
	} else {
		t.FolderName = FolderFromFilename(t.Filename)
	}
	t.SaveDir = filepath.Clean(filepath.Join(baseSaveDir, t.FolderName))
	t.FilePath = filepath.Clean(filepath.Join(t.SaveDir, t.Filename))
	return t
}

func (t *Task) setFilename(name string) {
	t.Filename = name
	t.FilePath = filepath.Clean(filepath.Join(t.SaveDir, name))
}

var inFlight = map[string]bool{
	StDownloading: true, StPending: true, StStarting: true, StResolving: true,
	StPausing: true, StSolving: true, StWaitingUser: true,
}

// TaskFromRecord restores a persisted task; one that was active when the app
// closed comes back Paused so it doesn't auto-start.
func TaskFromRecord(r store.TaskRecord) *Task {
	t := NewTask(r.Link, r.BaseSaveDir, r.FolderName)
	if inFlight[r.Status] {
		t.Status, t.PauseFlag = StPaused, true
	} else {
		t.Status = r.Status
	}
	if r.UID != "" {
		t.UID = r.UID
	}
	t.DownloadedBytes, t.TotalBytes, t.Progress = r.DownloadedBytes, r.TotalBytes, r.Progress
	t.ErrorMessage = r.ErrorMessage
	t.IsUpdate, t.UpdateVersion, t.InstallHandled = r.IsUpdate, r.UpdateVersion, r.InstallHandled
	// A name taken from the server (links like bzzhr.to/<id> carry none).
	if r.Filename != "" && r.Filename != t.Filename {
		t.setFilename(r.Filename)
	}
	return t
}

// Record is the persisted form of t.
func (t *Task) Record() store.TaskRecord {
	return store.TaskRecord{
		UID: t.UID, Link: t.Link, BaseSaveDir: t.BaseSaveDir, FolderName: t.FolderName,
		Status: t.Status, ErrorMessage: t.ErrorMessage, DownloadedBytes: t.DownloadedBytes,
		TotalBytes: t.TotalBytes, Progress: t.Progress, IsUpdate: t.IsUpdate,
		UpdateVersion: t.UpdateVersion, InstallHandled: t.InstallHandled, Filename: t.Filename,
	}
}

// View is a read-only copy of a task for the UI.
type View struct {
	UID, Link, FolderName, Filename, SaveDir, FilePath string
	Status, ErrorMessage                               string
	Progress, Speed                                    float64
	DownloadedBytes, TotalBytes                        int64
	IsUpdate, InstallHandled                           bool
	UpdateVersion                                      string
	ExtractProgress                                    float64
	HasExtractProgress                                 bool
}

func (t *Task) view() View {
	v := View{UID: t.UID, Link: t.Link, FolderName: t.FolderName, Filename: t.Filename,
		SaveDir: t.SaveDir, FilePath: t.FilePath, Status: t.Status, ErrorMessage: t.ErrorMessage,
		Progress: t.Progress, Speed: t.Speed, DownloadedBytes: t.DownloadedBytes,
		TotalBytes: t.TotalBytes, IsUpdate: t.IsUpdate, InstallHandled: t.InstallHandled}
	if t.UpdateVersion != nil {
		v.UpdateVersion = *t.UpdateVersion
	}
	if t.ExtractProgress != nil {
		v.ExtractProgress, v.HasExtractProgress = *t.ExtractProgress, true
	}
	return v
}
