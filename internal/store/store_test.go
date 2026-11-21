package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withDir(t *testing.T) string {
	old := Dir
	Dir = t.TempDir()
	t.Cleanup(func() { Dir = old })
	return Dir
}

func TestSettingsDefaultsAndUnknownKeys(t *testing.T) {
	d := withDir(t)
	in := `{"theme": "Light", "max_workers": 5.0, "bogus_type": 1, "captcha_timeout": "x",
	        "my_future_key": {"a": [1, 2]}, "bandwidth_limit": 7,
	        "schedule": {"enabled": true, "start": "23:00", "end": "05:00", "recurrence": "weekly", "days": [], "date": "", "wake_timer": false, "keep_awake": true, "targets": null},
	        "pending_update": {"zip": "C:\\z.zip", "version": "v2"}}`
	os.WriteFile(filepath.Join(d, ".silverspoon_settings.json"), []byte(in), 0o644)
	s := LoadSettings()
	if s.Theme != "Light" || s.MaxWorkers != 5 || s.MaxConnections != 4 || s.CaptchaTimeout != 10 {
		t.Errorf("%+v", s)
	}
	if s.BandwidthLimit != 7 || s.Schedule == nil || s.Schedule.Days == nil || len(*s.Schedule.Days) != 0 {
		t.Errorf("optional keys: %+v %+v", s, s.Schedule)
	}
	if s.PendingUpdate == nil || s.PendingUpdate.Version != "v2" {
		t.Errorf("pending %+v", s.PendingUpdate)
	}
	out, _ := os.ReadFile(SettingsPath())
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["my_future_key"]; !ok {
		t.Error("unknown key dropped")
	}
	if !strings.HasPrefix(string(out), "{\n    \"theme\": \"Light\",") {
		t.Errorf("format: %.60s", out)
	}
	// "days": [] must survive (empty = never), not turn into "all days".
	if sc := m["schedule"].(map[string]any); sc["days"] == nil {
		t.Error("empty days lost")
	}
	s.PendingUpdate = nil
	SaveSettings(s)
	out, _ = os.ReadFile(SettingsPath())
	if strings.Contains(string(out), "pending_update") {
		t.Error("cleared pending_update still written")
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	withDir(t)
	v := "v2.0.0"
	recs := []TaskRecord{
		{UID: "a", Link: "https://fuckingfast.co/x#G.part01.rar", BaseSaveDir: `C:\D`, FolderName: "G",
			Status: "Paused", DownloadedBytes: 5, TotalBytes: 10, Progress: 50},
		{UID: "b", Link: "https://example.com/u.zip", Status: "Completed", IsUpdate: true, UpdateVersion: &v},
	}
	if err := SaveHistory(recs); err != nil {
		t.Fatal(err)
	}
	got := LoadHistory()
	if !reflect.DeepEqual(got, recs) {
		t.Errorf("%+v", got)
	}
}

// TestRealFilesRoundTrip loads the user's actual settings and history (when
// present), saves them through the Go store into a temp dir, and checks the
// JSON means the same thing.
func TestRealFilesRoundTrip(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, name := range []string{".silverspoon_settings.json", ".silverspoon_history.json"} {
		orig, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Logf("skip %s: %v", name, err)
			continue
		}
		d := withDir(t)
		os.WriteFile(filepath.Join(d, name), orig, 0o644)
		if strings.Contains(name, "settings") {
			LoadSettings()
		} else {
			SaveHistory(LoadHistory())
		}
		after, _ := os.ReadFile(filepath.Join(d, name))
		var a, b any
		json.Unmarshal(orig, &a)
		json.Unmarshal(after, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s changed meaning after a Go round trip", name)
		}
	}
}
