// Package store reads and writes ~/.silverspoon_settings.json and
// ~/.silverspoon_history.json with the same keys and defaults as the Python
// app, so either build can pick up the other's queue and preferences.
package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"silverspoon/internal/scheduler"
)

// Dir holds the dot-files; it defaults to the user's home directory.
var Dir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}()

// SettingsPath is ~/.silverspoon_settings.json.
func SettingsPath() string { return filepath.Join(Dir, ".silverspoon_settings.json") }

// HistoryPath is ~/.silverspoon_history.json.
func HistoryPath() string { return filepath.Join(Dir, ".silverspoon_history.json") }

// LogPath is ~/.silverspoon.log.
func LogPath() string { return filepath.Join(Dir, ".silverspoon.log") }

// LockPath is ~/.silverspoon.lock (or .silverspoon2.lock when that path is a
// directory, as in the Python app).
func LockPath() string {
	p := filepath.Join(Dir, ".silverspoon.lock")
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return filepath.Join(Dir, ".silverspoon2.lock")
	}
	return p
}

// PendingUpdate is an update zip the user chose to install on next open.
type PendingUpdate struct {
	Zip     string `json:"zip"`
	Version string `json:"version"`
}

// Settings mirrors the Python settings dict. Unknown keys are kept in Extra
// and written back untouched.
type Settings struct {
	Theme                  string
	DefaultSaveDir         string
	MaxWorkers             int
	MaxConnections         int
	BindAddresses          string
	ExtractAfterDownload   bool
	AutoRetryErrors        bool
	CaptchaTimeout         int
	ShowSolverBrowser      bool
	ColumnWidths           map[string]int
	SkipDeleteConfirmation bool
	ShowWarningDialog      bool
	LastUpdateCheck        float64
	BandwidthLimit         int
	Schedule               *scheduler.Schedule
	PendingUpdate          *PendingUpdate

	// hasBandwidth etc. record optional keys, so they're only written when
	// present (as the Python app does).
	hasBandwidth bool
	Extra        map[string]json.RawMessage
	keyOrder     []string
}

func defaultDownloads() string {
	if runtime.GOOS == "windows" {
		d := filepath.Join(Dir, "Downloads")
		if err := os.MkdirAll(d, 0o755); err == nil {
			return d
		}
	}
	abs, _ := filepath.Abs(".")
	return abs
}

// Defaults returns the default settings.
func Defaults() *Settings {
	return &Settings{
		Theme: "Dark", DefaultSaveDir: defaultDownloads(), MaxWorkers: 3, MaxConnections: 4,
		CaptchaTimeout: 10, ColumnWidths: map[string]int{}, ShowWarningDialog: true,
		Extra: map[string]json.RawMessage{},
	}
}

var defaultKeys = []string{"theme", "default_save_dir", "max_workers", "max_connections",
	"bind_addresses", "extract_after_download", "auto_retry_errors", "captcha_timeout",
	"show_solver_browser", "column_widths", "skip_delete_confirmation", "show_warning_dialog",
	"last_update_check"}

// set decodes one known key into s; a value of the wrong type is ignored
// and the default kept. It reports whether key is known.
func (s *Settings) set(key string, raw json.RawMessage) bool {
	var num float64
	switch key {
	case "theme":
		json.Unmarshal(raw, &s.Theme)
	case "default_save_dir":
		json.Unmarshal(raw, &s.DefaultSaveDir)
	case "max_workers":
		if json.Unmarshal(raw, &num) == nil {
			s.MaxWorkers = int(num)
		}
	case "max_connections":
		if json.Unmarshal(raw, &num) == nil {
			s.MaxConnections = int(num)
		}
	case "bind_addresses":
		json.Unmarshal(raw, &s.BindAddresses)
	case "extract_after_download":
		json.Unmarshal(raw, &s.ExtractAfterDownload)
	case "auto_retry_errors":
		json.Unmarshal(raw, &s.AutoRetryErrors)
	case "captcha_timeout":
		if json.Unmarshal(raw, &num) == nil {
			s.CaptchaTimeout = int(num)
		}
	case "show_solver_browser":
		json.Unmarshal(raw, &s.ShowSolverBrowser)
	case "column_widths":
		var m map[string]float64
		if json.Unmarshal(raw, &m) == nil {
			s.ColumnWidths = map[string]int{}
			for k, v := range m {
				s.ColumnWidths[k] = int(v)
			}
		}
	case "skip_delete_confirmation":
		json.Unmarshal(raw, &s.SkipDeleteConfirmation)
	case "show_warning_dialog":
		json.Unmarshal(raw, &s.ShowWarningDialog)
	case "last_update_check":
		json.Unmarshal(raw, &s.LastUpdateCheck)
	case "bandwidth_limit":
		if json.Unmarshal(raw, &num) == nil {
			s.BandwidthLimit = int(num)
			s.hasBandwidth = true
		}
	case "schedule":
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			s.Schedule = nil
		} else {
			sc := scheduler.DefaultSchedule()
			if json.Unmarshal(raw, &sc) == nil {
				s.Schedule = &sc
			}
		}
	case "pending_update":
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			s.PendingUpdate = nil
		} else {
			var p PendingUpdate
			if json.Unmarshal(raw, &p) == nil {
				s.PendingUpdate = &p
			}
		}
	default:
		return false
	}
	return true
}

func (s *Settings) value(key string) any {
	switch key {
	case "theme":
		return s.Theme
	case "default_save_dir":
		return s.DefaultSaveDir
	case "max_workers":
		return s.MaxWorkers
	case "max_connections":
		return s.MaxConnections
	case "bind_addresses":
		return s.BindAddresses
	case "extract_after_download":
		return s.ExtractAfterDownload
	case "auto_retry_errors":
		return s.AutoRetryErrors
	case "captcha_timeout":
		return s.CaptchaTimeout
	case "show_solver_browser":
		return s.ShowSolverBrowser
	case "column_widths":
		if s.ColumnWidths == nil {
			return map[string]int{}
		}
		return s.ColumnWidths
	case "skip_delete_confirmation":
		return s.SkipDeleteConfirmation
	case "show_warning_dialog":
		return s.ShowWarningDialog
	case "last_update_check":
		return s.LastUpdateCheck
	case "bandwidth_limit":
		return s.BandwidthLimit
	case "schedule":
		return s.Schedule
	case "pending_update":
		return s.PendingUpdate
	}
	return s.Extra[key]
}

// orderedKeys lists the keys to write: defaults first, then any other keys in
// the order they were read or set.
func (s *Settings) orderedKeys() []string {
	keys := append([]string{}, defaultKeys...)
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k] = true
	}
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range s.keyOrder {
		if k == "bandwidth_limit" && !s.hasBandwidth {
			continue
		}
		if k == "schedule" && s.Schedule == nil {
			continue
		}
		if k == "pending_update" && s.PendingUpdate == nil {
			continue
		}
		if _, isExtra := s.Extra[k]; isExtra || k == "bandwidth_limit" || k == "schedule" || k == "pending_update" {
			add(k)
		}
	}
	if s.hasBandwidth {
		add("bandwidth_limit")
	}
	if s.Schedule != nil {
		add("schedule")
	}
	if s.PendingUpdate != nil {
		add("pending_update")
	}
	for k := range s.Extra {
		add(k)
	}
	return keys
}

// SetBandwidthLimit sets bandwidth_limit (MB/s, 0 = unlimited).
func (s *Settings) SetBandwidthLimit(v int) {
	s.BandwidthLimit, s.hasBandwidth = v, true
}

// MarshalJSON writes the settings in the Python key order.
func (s *Settings) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range s.orderedKeys() {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := marshalNoEscape(s.value(k))
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ParseSettings merges data over the defaults.
func ParseSettings(data []byte) *Settings {
	s := Defaults()
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return s
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return s
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return s
		}
		s.keyOrder = append(s.keyOrder, key)
		if !s.set(key, raw) {
			s.Extra[key] = raw
		}
	}
	return s
}

var settingsMu sync.Mutex

// LoadSettings reads the settings file over the defaults and writes the
// merged result back, as the Python app does on start.
func LoadSettings() *Settings {
	settingsMu.Lock()
	data, err := os.ReadFile(SettingsPath())
	settingsMu.Unlock()
	s := Defaults()
	if err == nil {
		s = ParseSettings(data)
	}
	SaveSettings(s)
	return s
}

// SaveSettings writes the settings file (indent 4).
func SaveSettings(s *Settings) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return writeJSON(SettingsPath(), s)
}

func writeJSON(path string, v any) error {
	raw, err := marshalNoEscape(v)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "    "); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TaskRecord is one persisted task, in the Python to_dict key order.
type TaskRecord struct {
	UID             string  `json:"uid"`
	Link            string  `json:"link"`
	BaseSaveDir     string  `json:"base_save_dir"`
	FolderName      string  `json:"folder_name"`
	Status          string  `json:"status"`
	ErrorMessage    string  `json:"error_message"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	Progress        float64 `json:"progress"`
	IsUpdate        bool    `json:"is_update"`
	UpdateVersion   *string `json:"update_version"`
	InstallHandled  bool    `json:"install_handled"`
	Filename        string  `json:"filename"`
}

var historyMu sync.Mutex

// LoadHistory reads the history file; a missing or broken file gives none.
func LoadHistory() []TaskRecord {
	historyMu.Lock()
	defer historyMu.Unlock()
	data, err := os.ReadFile(HistoryPath())
	if err != nil {
		return nil
	}
	var raw []json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	out := make([]TaskRecord, 0, len(raw))
	for _, r := range raw {
		var t TaskRecord
		if json.Unmarshal(r, &t) != nil || t.Link == "" {
			// Python's from_dict would fail on a broken entry and drop the
			// whole history; skipping just the entry is the kinder reading.
			continue
		}
		out = append(out, t)
	}
	return out
}

// SaveHistory writes the history file (indent 4).
func SaveHistory(tasks []TaskRecord) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	if tasks == nil {
		tasks = []TaskRecord{}
	}
	return writeJSON(HistoryPath(), tasks)
}
