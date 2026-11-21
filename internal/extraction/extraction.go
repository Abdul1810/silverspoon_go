// Package extraction holds the archive helpers for the auto-extractor
// (port of extraction.py). Listing uses 7-Zip's technical listing (-slt),
// which reads only the archive headers, so it is quick even for multi-GB
// multipart RARs.
package extraction

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ErrCancelled means the extractor was stopped because the user cancelled.
var ErrCancelled = errors.New("extraction cancelled")

// Entry is one file in an archive listing.
type Entry struct {
	Path string
	Size int64
}

var percentRE = regexp.MustCompile(`(\d{1,3})%`)

// ListArchive returns every file in the archive, or nil if 7-Zip can't read
// it (not an archive, missing volume, password, ...).
func ListArchive(sevenZip, archive string) []Entry {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sevenZip, "l", "-slt", "-ba", "-sccUTF-8", archive)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	entries := ParseListing(strings.ToValidUTF8(string(out), "�"))
	if entries == nil {
		entries = []Entry{}
	}
	return entries
}

// ParseListing returns the files (not folders) from `7z l -slt -ba` output.
func ParseListing(text string) []Entry {
	var entries []Entry
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, " = "); ok {
				fields[k] = v
			}
		}
		path := fields["Path"]
		if path == "" {
			continue
		}
		if fields["Folder"] == "+" || strings.HasPrefix(fields["Attributes"], "D") {
			continue
		}
		size, err := strconv.ParseInt(strings.TrimSpace(fields["Size"]), 10, 64)
		if err != nil {
			size = 0
		}
		entries = append(entries, Entry{path, size})
	}
	return entries
}

// IsExtracted reports whether every listed file already exists in destDir with
// the same size. A partial or interrupted extraction therefore doesn't count.
func IsExtracted(entries []Entry, destDir string) bool {
	if len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		st, err := os.Stat(filepath.Join(destDir, e.Path))
		if err != nil || st.Size() != e.Size {
			return false
		}
	}
	return true
}

// RunWithProgress runs an extractor, calling onProgress(percent) whenever its
// output reports one (7-Zip with -bsp1, unrar). It returns the exit code and
// the last 2000 bytes of output.
//
// If shouldStop() turns true the extractor is killed (checked every 0.2 s, so
// it works even while the extractor prints nothing) and ErrCancelled is
// returned.
func RunWithProgress(args []string, onProgress func(int), shouldStop func() bool) (int, string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return -1, "", err
	}

	var stopped atomic.Bool
	done := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close()
		waitErr <- err
	}()
	if shouldStop != nil {
		go func() {
			t := time.NewTicker(200 * time.Millisecond)
			defer t.Stop()
			for {
				if shouldStop() {
					stopped.Store(true)
					cmd.Process.Kill()
					return
				}
				select {
				case <-done:
					return
				case <-t.C:
				}
			}
		}()
	}

	var tail []byte
	buf := make([]byte, 4096)
	for {
		n, err := pr.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			tail = append(tail, chunk...)
			if len(tail) > 2000 {
				tail = append([]byte(nil), tail[len(tail)-2000:]...)
			}
			if found := percentRE.FindAllSubmatch(chunk, -1); len(found) > 0 {
				p, _ := strconv.Atoi(string(found[len(found)-1][1]))
				if p > 100 {
					p = 100
				}
				onProgress(p)
			}
		}
		if err != nil {
			break
		}
	}
	err := <-waitErr
	close(done)
	if stopped.Load() {
		return -1, "", ErrCancelled
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			return -1, "", err
		}
	}
	return code, strings.ToValidUTF8(string(tail), "�"), nil
}
