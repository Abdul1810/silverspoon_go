// Package applog writes ~/.silverspoon.log in the Python app's format
// ("2026-11-20 23:59:29,086 - ERROR - message"), rotating at 1 MB with five
// backups, so "Copy Error Details" reads the same file either build wrote.
package applog

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxBytes = 1_000_000
	backups  = 5
)

type rotating struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func (r *rotating) write(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		st, _ := f.Stat()
		r.f, r.size = f, st.Size()
	}
	if r.size+int64(len(line)) > maxBytes && r.size > 0 {
		r.f.Close()
		r.f = nil
		for i := backups - 1; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
		}
		os.Rename(r.path, r.path+".1")
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		r.f, r.size = f, 0
	}
	n, _ := r.f.WriteString(line)
	r.size += int64(n)
}

type handler struct {
	out   *rotating
	min   slog.Level
	attrs []slog.Attr
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.min }

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	}
	return "DEBUG"
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	fmt.Fprintf(&b, "%s,%03d - %s - %s", t.Format("2006-01-02 15:04:05"), t.Nanosecond()/1e6, levelName(r.Level), r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	b.WriteByte('\n')
	h.out.write(b.String())
	return nil
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	return &handler{out: h.out, min: h.min, attrs: append(append([]slog.Attr{}, h.attrs...), as...)}
}

func (h *handler) WithGroup(string) slog.Handler { return h }

// New returns loggers writing to path: app at ERROR (as the Python root
// logger) and solver at INFO (cf_turnstile's own level).
func New(path string) (app, solver *slog.Logger) {
	out := &rotating{path: path}
	return slog.New(&handler{out: out, min: slog.LevelError}), slog.New(&handler{out: out, min: slog.LevelInfo})
}
