//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// ReadClipboard returns the clipboard's HTML (if a helper can read it) and
// plain text.
func ReadClipboard() (html, text string) {
	try := func(name string, args ...string) string {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	if runtime.GOOS == "darwin" {
		return "", try("pbpaste")
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return try("wl-paste", "-t", "text/html"), try("wl-paste", "-n")
	}
	return try("xclip", "-selection", "clipboard", "-t", "text/html", "-o"),
		try("xclip", "-selection", "clipboard", "-o")
}

// Open opens a folder or URL.
func Open(target string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", target).Start()
	}
	return exec.Command("xdg-open", target).Start()
}

// MessageBox prints the message (no native box before the window exists).
func MessageBox(title, text string) { fmt.Fprintf(os.Stderr, "%s: %s\n", title, text) }

func pick(args ...string) (string, bool) {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("osascript", "-e", args[len(args)-1]).Output()
		p := strings.TrimSpace(string(out))
		return p, err == nil && p != ""
	}
	out, err := exec.Command("zenity", args[:len(args)-1]...).Output()
	p := strings.TrimSpace(string(out))
	return p, err == nil && p != ""
}

// PickFolder shows a folder picker (zenity / osascript).
func PickFolder(title, start string) (string, bool) {
	return pick("--file-selection", "--directory", "--title="+title, "--filename="+start+"/",
		`POSIX path of (choose folder with prompt "`+title+`")`)
}

// PickFile shows a text-file picker.
func PickFile(title string) (string, bool) {
	return pick("--file-selection", "--title="+title, "--file-filter=Text files | *.txt", "--file-filter=All files | *",
		`POSIX path of (choose file with prompt "`+title+`")`)
}

// Lock is the single-instance lock (flock, as QLockFile uses on Unix).
type Lock struct{ f *os.File }

// ErrLocked means another instance holds the lock.
var ErrLocked = errors.New("already running")

// AcquireLock takes the lock at path.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrLocked
	}
	f.Truncate(0)
	fmt.Fprintf(f, "%d\nSilverSpoon\n", os.Getpid())
	return &Lock{f: f}, nil
}

// Release frees the lock.
func (l *Lock) Release() {
	if l != nil {
		l.f.Close()
	}
}
