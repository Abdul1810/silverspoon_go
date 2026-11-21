package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"silverspoon/internal/providers"
	"silverspoon/internal/segmented"
	"silverspoon/internal/transport"
)

var contentRangeTotal = regexp.MustCompile(`/([0-9]+)$`)

// speedMeter is a 3-second sliding window over (time, bytes) samples.
type speedMeter struct {
	ts []time.Time
	bs []int64
}

func (m *speedMeter) add(now time.Time, done int64) float64 {
	m.ts = append(m.ts, now)
	m.bs = append(m.bs, done)
	for len(m.ts) > 1 && now.Sub(m.ts[0]) > 3*time.Second {
		m.ts, m.bs = m.ts[1:], m.bs[1:]
	}
	dur := now.Sub(m.ts[0]).Seconds()
	if dur <= 0 {
		return -1
	}
	return float64(done-m.bs[0]) / dur / (1024 * 1024)
}

// worker runs one task. App updates and any non-resolver URL download straight
// over HTTP; form hosts (FileKeeper) first trade the page for a direct link.
// Only FuckingFast/DataNodes/BZZHR links go through the solver.
func (e *Engine) worker(t *Task) {
	e.mu.Lock()
	link, isUpdate := t.Link, t.IsUpdate
	e.mu.Unlock()

	if isUpdate || !providers.NeedsResolution(link) {
		url := ""
		if !isUpdate && providers.NeedsFormResolution(link) {
			url = e.resolveFormLink(t)
			if url == "" {
				return
			}
		}
		e.downloadDirectFile(t, url)
		return
	}

	dlURL := e.getDirectLink(t)
	if dlURL == "" {
		e.update(func() {
			if t.CancelFlag || t.PauseFlag {
				return
			}
			// Don't auto-retry a CAPTCHA timeout: re-solving immediately just
			// respawns the browser and rarely succeeds.
			if e.cfg().AutoRetry && t.RetryCount < 3 && t.Status != StCaptchaTimeout {
				t.RetryCount++
				t.Status, t.ErrorMessage = StPending, ""
				Log.Info(fmt.Sprintf("Auto-retrying %s (attempt %d/3) after missing direct link", t.Filename, t.RetryCount))
				return
			}
			if t.Status != StCaptchaTimeout {
				t.Status = StError
			}
			if t.ErrorMessage == "" {
				t.ErrorMessage = "Could not get the direct download link."
			}
		})
		return
	}

	stop := false
	e.update(func() {
		if t.CancelFlag {
			t.Status, stop = StCancelled, true
		} else if t.PauseFlag {
			t.Status, stop = StPaused, true
		}
	})
	if stop {
		return
	}

	if providers.DownloadResolvedDirectly(link) {
		// bzzhr.to: the resolved link needs no browser cookies, so use the
		// direct downloader (resume, multiple connections, server file name).
		e.downloadDirectFile(t, dlURL)
		return
	}
	e.downloadSolved(t, dlURL)
}

func (e *Engine) getDirectLink(t *Task) string {
	e.update(func() { t.Status = StSolving })
	if e.Solver == nil {
		e.update(func() {
			t.Status, t.ErrorMessage = StError, "Could not get the direct download link. No solver available."
		})
		return ""
	}
	res, err := e.Solver.GetDirectLink(t.Link, func(waiting bool) {
		// The solver shows its window when a check needs a human click.
		e.update(func() {
			if waiting {
				t.Status = StWaitingUser
			} else {
				t.Status = StSolving
			}
		})
	})
	if err != nil {
		Log.Error(fmt.Sprintf("Error getting direct link for %s: %v", t.Link, err))
		msg := err.Error()
		e.update(func() {
			if strings.Contains(msg, "after ") && strings.Contains(msg, " seconds") {
				t.Status = StCaptchaTimeout
			} else {
				t.Status = StError
			}
			t.ErrorMessage = "Could not get the direct download link. " + FormatError(err)
		})
		return ""
	}
	if res.DirectURL == "" {
		e.update(func() {
			t.Status = StCaptchaTimeout
			t.ErrorMessage = "The file host did not return a direct download link. The link may be expired or unavailable."
		})
		return ""
	}
	e.update(func() { t.dlCookies, t.dlUserAgent = res.Cookies, res.UserAgent })
	return res.DirectURL
}

// resolveFormLink returns the direct URL for a form host, or "" if it failed
// or the task was paused/cancelled during the countdown.
func (e *Engine) resolveFormLink(t *Task) string {
	wait := func(seconds int) {
		end := time.Now().Add(time.Duration(seconds) * time.Second)
		for time.Now().Before(end) {
			if p, c := e.flags(t); p || c {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	url, err := providers.ResolveFormLink(t.Link, wait, 30*time.Second)
	if err != nil {
		Log.Error(fmt.Sprintf("Form host resolution error for %s: %v", t.Link, err))
		save := false
		e.update(func() {
			if t.CancelFlag || t.PauseFlag {
				return
			}
			save = true
			if e.cfg().AutoRetry && t.RetryCount < 3 {
				t.RetryCount++
				t.Status, t.ErrorMessage = StPending, ""
				return
			}
			t.Status = StError
			var re *providers.ResolveError
			detail := FormatError(err)
			if errors.As(err, &re) {
				detail = re.Error()
			}
			t.ErrorMessage = "Could not get the direct download link. " + detail
		})
		if save {
			e.RequestSave()
		}
		return ""
	}
	if p, c := e.flags(t); p || c {
		return "" // PauseTasks/CancelTasks already set the status
	}
	return url
}

func (e *Engine) failed(t *Task, err error) {
	Log.Error(fmt.Sprintf("Direct download error for %s: %v", t.Link, err))
	save := false
	e.update(func() {
		if t.CancelFlag || t.PauseFlag {
			return
		}
		save = true
		if e.cfg().AutoRetry && t.RetryCount < 3 {
			t.RetryCount++
			t.Status, t.ErrorMessage = StPending, ""
			return
		}
		t.Status = StError
		what := "Download"
		if t.IsUpdate {
			what = "Update download"
		}
		t.ErrorMessage = what + " failed. " + FormatError(err)
	})
	if save {
		e.RequestSave()
	}
}

func (e *Engine) completed(t *Task) {
	e.update(func() {
		t.Progress, t.Speed, t.Status, t.ErrorMessage = 100, 0, StCompleted, ""
	})
	e.RequestSave()
}

// downloadDirectFile fetches a plain URL over net/http with resume,
// pause/cancel and progress. Used for app updates, form hosts (url is the
// resolved link), bzzhr's resolved link and any non-resolver host.
func (e *Engine) downloadDirectFile(t *Task, url string) {
	e.mu.Lock()
	saveDir := t.SaveDir
	if url == "" {
		url = t.Link
	}
	e.mu.Unlock()
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		e.update(func() {
			t.Status = StError
			t.ErrorMessage = fmt.Sprintf("Failed to create download folder '%s'. %s", saveDir, FormatError(err))
		})
		e.RequestSave()
		return
	}
	headers := map[string]string{"User-Agent": "SilverSpoon/" + Version}
	e.adoptServerFilename(t, url, headers)
	if e.downloadSegmented(t, url, headers, nil) {
		return
	}

	e.mu.Lock()
	path := t.FilePath
	e.mu.Unlock()
	var initial int64
	if st, err := os.Stat(path); err == nil {
		initial = st.Size()
	}
	h := map[string]string{"User-Agent": headers["User-Agent"]}
	if initial > 0 {
		h["Range"] = fmt.Sprintf("bytes=%d-", initial)
	}
	e.update(func() { t.Status, t.ErrorMessage = StDownloading, "" })

	r, err := segmented.DefaultOpener(url, h, 30*time.Second, "")
	if err != nil {
		if segmented.StatusCode(err) == 416 && initial > 0 {
			// Range not satisfiable -> the file is already fully downloaded.
			e.update(func() { t.TotalBytes, t.DownloadedBytes = initial, initial })
			e.completed(t)
			return
		}
		e.failed(t, err)
		return
	}
	defer r.Body.Close()
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if r.Status == 206 && r.Header("Content-Range") != "" {
		var total int64
		if m := contentRangeTotal.FindStringSubmatch(r.Header("Content-Range")); m != nil {
			total, _ = strconv.ParseInt(m[1], 10, 64)
		}
		e.update(func() { t.TotalBytes = total })
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	} else {
		// Server ignored Range (or none asked) -> full body, restart.
		cl, _ := strconv.ParseInt(r.Header("Content-Length"), 10, 64)
		e.update(func() { t.TotalBytes = cl })
		initial = 0
	}
	e.update(func() { t.DownloadedBytes = initial })
	if err := e.stream(t, r.Body, path, flag, 0); err != nil {
		e.failed(t, err)
	}
}

// stream copies body into path, updating progress and speed, honouring
// pause/cancel and an optional bytes-per-second limit. It marks the task
// Completed on success; returns nil also when paused/cancelled.
func (e *Engine) stream(t *Task, body io.Reader, path string, flag int, limit float64) error {
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	var meter speedMeter
	e.mu.Lock()
	meter.add(time.Now(), t.DownloadedBytes)
	e.mu.Unlock()
	lastTime := time.Now()
	var sinceLast int64
	buf := make([]byte, 64*1024)
	for {
		stopped := false
		e.update(func() {
			if t.PauseFlag {
				t.Status, t.Speed, stopped = StPaused, 0, true
			} else if t.CancelFlag {
				t.Status, t.Speed, stopped = StCancelled, 0, true
			}
		})
		if stopped {
			return nil
		}
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			sinceLast += int64(n)
			now := time.Now()
			if limit > 0 {
				expected := time.Duration(float64(sinceLast) / limit * float64(time.Second))
				if actual := now.Sub(lastTime); expected > actual {
					time.Sleep(expected - actual)
					now = time.Now()
				}
			}
			e.update(func() {
				t.DownloadedBytes += int64(n)
				if s := meter.add(now, t.DownloadedBytes); s >= 0 {
					t.Speed = s
				}
				if t.TotalBytes > 0 {
					t.Progress = float64(t.DownloadedBytes) / float64(t.TotalBytes) * 100
				}
			})
			if now.Sub(lastTime) > 500*time.Millisecond {
				lastTime, sinceLast = now, 0
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	e.completed(t)
	return nil
}

// adoptServerFilename: links like bzzhr.to/<id> carry no file name, so before
// anything is written, take the name the server sends (Content-Disposition).
func (e *Engine) adoptServerFilename(t *Task, url string, headers map[string]string) {
	e.mu.Lock()
	name, path := t.Filename, t.FilePath
	e.mu.Unlock()
	if filepath.Ext(name) != "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return
	}
	if _, err := os.Stat(segmented.StatePath(path)); err == nil {
		return
	}
	h := map[string]string{"Range": "bytes=0-0"}
	for k, v := range headers {
		h[k] = v
	}
	r, err := segmented.DefaultOpener(url, h, 30*time.Second, "")
	if err != nil {
		Log.Warn(fmt.Sprintf("Could not read the server file name for %s: %v", t.Link, err))
		return
	}
	cd := r.Header("Content-Disposition")
	r.Body.Close()
	if n := providers.FilenameFromContentDisposition(cd); n != "" && n != name {
		e.update(func() { t.setFilename(n) })
		e.RequestSave()
	}
}

func (e *Engine) bytesPerSecond() float64 {
	c := e.cfg()
	if c.BandwidthLimit > 0 && c.MaxWorkers > 0 {
		return float64(c.BandwidthLimit) * 1024 * 1024 / float64(c.MaxWorkers)
	}
	return 0
}

// downloadSegmented downloads over several connections when the server
// supports ranges. It returns true when it handled the task (finished,
// paused, cancelled or failed) and false to fall back to one connection.
func (e *Engine) downloadSegmented(t *Task, url string, headers map[string]string, opener segmented.Opener) bool {
	c := e.cfg()
	connections := c.MaxConnections
	if connections < 1 {
		connections = 1
	}
	sources := transport.ParseSources(c.BindAddresses)
	e.mu.Lock()
	path := t.FilePath
	e.mu.Unlock()
	state := segmented.StatePath(path)
	_, serr := os.Stat(state)
	resuming := serr == nil
	if connections <= 1 && !resuming {
		return false
	}
	total, ranges, err := segmented.Probe(url, headers, 30*time.Second, opener)
	if err != nil {
		if !resuming {
			return false // let the single-connection path report it
		}
		e.failed(t, err)
		return true
	}
	if !ranges || total <= 0 {
		if resuming {
			// A half-done segmented file can't be continued without ranges;
			// its size isn't the downloaded byte count, so start over.
			os.Remove(state)
			os.Remove(path)
		}
		return false
	}
	if total < 2*segmented.MinSegment && !resuming {
		return false // too small to be worth splitting
	}

	e.update(func() { t.Status, t.ErrorMessage, t.TotalBytes = StDownloading, "", total })
	var meter speedMeter
	d := &segmented.Download{
		URL: url, Path: path, Total: total, Connections: connections, Headers: headers,
		ShouldStop: func() bool { p, c := e.flags(t); return p || c },
		OnProgress: func(done int64) {
			now := time.Now()
			e.update(func() {
				t.DownloadedBytes = done
				t.Progress = float64(done) / float64(total) * 100
				if s := meter.add(now, done); s >= 0 {
					t.Speed = s
				}
			})
		},
		BytesPerSecond: e.bytesPerSecond(), Opener: opener, Sources: sources,
	}
	finished, err := d.Run()
	e.update(func() { t.Speed = 0 })
	if err != nil {
		e.failed(t, err)
		return true
	}
	if !finished {
		// Progress is saved beside the file; Start/Resume continues it.
		e.update(func() {
			if t.CancelFlag {
				t.Status = StCancelled
			} else {
				t.Status = StPaused
			}
		})
		return true
	}
	e.update(func() { t.DownloadedBytes = total })
	e.completed(t)
	return true
}

// downloadSolved fetches a solver-resolved link with the browser's cookies,
// UA and TLS fingerprint: multi-connection first, else one stream.
func (e *Engine) downloadSolved(t *Task, dlURL string) {
	e.update(func() { t.Status, t.ErrorMessage = StDownloading, "" })
	e.mu.Lock()
	saveDir, path, cookies, ua := t.SaveDir, t.FilePath, t.dlCookies, t.dlUserAgent
	e.mu.Unlock()

	retryOrFail := func(msg string, logLine string) {
		save := false
		e.update(func() {
			save = true
			if e.cfg().AutoRetry && t.RetryCount < 3 {
				t.RetryCount++
				t.Status, t.ErrorMessage = StPending, ""
				Log.Info(fmt.Sprintf("Auto-retrying %s (attempt %d/3)%s", t.Filename, t.RetryCount, logLine))
				return
			}
			t.Status, t.ErrorMessage = StError, msg
		})
		if save {
			e.RequestSave()
		}
	}

	if _, err := os.Stat(saveDir); err != nil {
		if err := os.MkdirAll(saveDir, 0o755); err != nil {
			retryOrFail(fmt.Sprintf("Failed to create save directory '%s'. %s", saveDir, FormatError(err)), " after directory error")
			return
		}
	}
	var initial int64
	if st, err := os.Stat(path); err == nil {
		initial = st.Size()
	}
	headers := map[string]string{}
	if ua != "" {
		headers["User-Agent"] = ua
	}
	if e.downloadSegmented(t, dlURL, headers, transport.ChromeOpener(cookies)) {
		return
	}

	workerFailed := func(err error) {
		Log.Error(fmt.Sprintf("Download worker error for task %s: %v", t.Link, err))
		if p, c := e.flags(t); p || c {
			return
		}
		retryOrFail("Download failed. "+FormatError(err), "")
	}

	ctx := context.Background()
	var total int64
	// Some hosts reject HEAD while accepting the ranged GET. Only trust
	// Content-Length when the HEAD itself succeeded.
	if status, hdr, err := transport.ChromeHead(ctx, dlURL, headers, cookies, 30*time.Second, ""); err == nil &&
		status >= 200 && status < 300 {
		total, _ = strconv.ParseInt(hdr.Get("Content-Length"), 10, 64)
	}
	e.update(func() { t.TotalBytes = total })
	if initial > 0 && initial == total {
		e.update(func() {
			t.DownloadedBytes, t.Progress, t.Status, t.ErrorMessage = total, 100, StCompleted, ""
		})
		return
	}
	rh := map[string]string{}
	for k, v := range headers {
		rh[k] = v
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if initial > 0 {
		rh["Range"] = fmt.Sprintf("bytes=%d-", initial)
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	resp, err := transport.ChromeClient("").R().SetContext(ctx).SetHeaders(rh).
		SetCookies(cookieList(cookies)...).DisableAutoReadResponse().Get(dlURL)
	if err != nil {
		workerFailed(err)
		return
	}
	defer resp.Body.Close()
	code := resp.StatusCode
	if code == 416 && initial > 0 {
		if m := contentRangeTotal.FindStringSubmatch(resp.Header.Get("Content-Range")); m != nil {
			if n, _ := strconv.ParseInt(m[1], 10, 64); n == initial {
				e.update(func() { t.TotalBytes, t.DownloadedBytes = initial, initial })
				e.completed(t)
				return
			}
		}
	}
	if code != 200 && code != 206 {
		if code == 403 || code == 503 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			Log.Error(fmt.Sprintf("Download 403/503 for %s. Body preview: %s", dlURL, preview))
		}
		retryOrFail(fmt.Sprintf("Download request failed. Server returned HTTP %d.", code), fmt.Sprintf(" after HTTP %d", code))
		return
	}
	if code == 200 && initial > 0 {
		// server ignores range header, restart from beginning
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		initial = 0
	}
	e.update(func() { t.DownloadedBytes = initial })
	if total == 0 {
		var tb int64
		if m := contentRangeTotal.FindStringSubmatch(resp.Header.Get("Content-Range")); m != nil {
			tb, _ = strconv.ParseInt(m[1], 10, 64)
		} else if cl, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64); err == nil {
			tb = cl + initial
		}
		e.update(func() { t.TotalBytes = tb })
	}
	if err := e.stream(t, resp.Body, path, flag, e.bytesPerSecond()); err != nil {
		workerFailed(err)
	}
}
