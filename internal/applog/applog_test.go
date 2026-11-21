package applog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFormatLevelsAndRotation(t *testing.T) {
	// Not t.TempDir: the logger keeps the file open, which blocks cleanup on Windows.
	dir, _ := os.MkdirTemp("", "applog")
	p := filepath.Join(dir, ".silverspoon.log")
	app, solver := New(p)
	app.Info("hidden: app logs errors only")
	app.Error("Direct download error for x: boom")
	solver.Info("TurnstileSolver: resolving x")
	b, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	re := regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d,\d{3} - (ERROR|INFO) - `)
	for _, l := range lines {
		if !re.MatchString(l) {
			t.Errorf("format %q", l)
		}
	}
	big := strings.Repeat("x", 4000)
	for i := 0; i < 300; i++ {
		app.Error(big)
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Error("log did not rotate")
	}
	if st, _ := os.Stat(p); st.Size() > maxBytes {
		t.Errorf("log is %d bytes", st.Size())
	}
}
