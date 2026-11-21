// Package segmented implements multi-connection HTTP downloads (port of
// segmented.py).
//
// A file is split into byte ranges fetched in parallel, each written straight
// into its place in one file. On Windows that file is made sparse first, so
// writing at a far offset doesn't make NTFS zero-fill everything before it.
// Progress lives in a small "<file>.segments.json" next to the download, so
// pause and app restarts resume where every segment stopped.
package segmented

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	// StateSuffix is appended to the download path for the progress file.
	StateSuffix = ".segments.json"
	// MinSegment: never split work into pieces smaller than this.
	MinSegment = 4 * 1024 * 1024
	// Chunk is the read size per iteration.
	Chunk = 64 * 1024
	// MaxWorkerFailures is how many consecutive errors a worker tolerates.
	MaxWorkerFailures = 5
)

// ThrottleCodes are statuses a host uses to say "too many connections"; the
// download carries on with fewer instead of failing.
var ThrottleCodes = map[int]bool{403: true, 429: true, 503: true}

// FatalCodes mean the link itself is gone (expired/removed): stop now.
var FatalCodes = map[int]bool{401: true, 404: true, 410: true}

// RangeNotSupportedError: the server ignored a Range request, so the file
// can't be segmented.
type RangeNotSupportedError struct{ Msg string }

func (e *RangeNotSupportedError) Error() string { return e.Msg }

// HTTPStatusError is a non-success HTTP status from an opener.
type HTTPStatusError struct{ Code int }

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// StatusCode returns the HTTP status carried by err, or 0.
func StatusCode(err error) int {
	var he *HTTPStatusError
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

// ErrConnectionClosed means a range response ended early.
var ErrConnectionClosed = errors.New("connection closed before the range was complete")

// StatePath is the progress file for a download path.
func StatePath(path string) string { return path + StateSuffix }

// Response is what an Opener returns. Body must be closed by the caller.
type Response struct {
	Status int
	Header func(name string) string
	Body   io.ReadCloser
}

// Opener performs a GET. source is a local IP to send from ("" lets the OS
// choose). It returns *HTTPStatusError for statuses >= 400.
type Opener func(url string, headers map[string]string, timeout time.Duration, source string) (*Response, error)

var (
	contentRangeTotal = regexp.MustCompile(`/(\d+)\s*$`)
	contentRangeStart = regexp.MustCompile(`^bytes (\d+)-`)
)

func copyHeaders(h map[string]string, extra ...string) map[string]string {
	out := make(map[string]string, len(h)+len(extra)/2)
	for k, v := range h {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

// Probe returns (total size, accepts ranges) from a 1-byte Range request.
// total is 0 when the server doesn't say.
func Probe(url string, headers map[string]string, timeout time.Duration, opener Opener) (int64, bool, error) {
	if opener == nil {
		opener = DefaultOpener
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	r, err := opener(url, copyHeaders(headers, "Range", "bytes=0-0"), timeout, "")
	if err != nil {
		return 0, false, err
	}
	defer r.Body.Close()
	status := r.Status
	if status == 0 {
		status = 200
	}
	if m := contentRangeTotal.FindStringSubmatch(r.Header("Content-Range")); status == 206 && m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			return n, true, nil
		}
	}
	n, err := strconv.ParseInt(r.Header("Content-Length"), 10, 64)
	if err != nil {
		return 0, false, nil
	}
	return n, false, nil
}

// throttle is a shared bytes-per-second budget across a download's connections.
type throttle struct {
	rate float64
	mu   sync.Mutex
	next time.Time
}

func (t *throttle) consume(n int, stop <-chan struct{}) {
	if t.rate <= 0 {
		return
	}
	t.mu.Lock()
	now := time.Now()
	start := now
	if t.next.After(now) {
		start = t.next
	}
	t.next = start.Add(time.Duration(float64(n) / t.rate * float64(time.Second)))
	wait := start.Sub(now)
	t.mu.Unlock()
	if wait > 0 {
		sleepOrStop(wait, stop)
	}
}

func sleepOrStop(d time.Duration, stop <-chan struct{}) {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-tm.C:
	case <-stop:
	}
}

type segment struct {
	start, end, pos int64
	owner           int // -1 when unowned
}

func (s *segment) remaining() int64 {
	if r := s.end - s.pos; r > 0 {
		return r
	}
	return 0
}

// Download fetches URL into Path over up to Connections parallel Range
// requests.
type Download struct {
	URL         string
	Path        string
	Total       int64
	Connections int
	Headers     map[string]string
	// ShouldStop is polled ~4x a second; when true the download pauses.
	ShouldStop func() bool
	// OnProgress(downloaded) is called a few times a second from Run's goroutine.
	OnProgress     func(int64)
	BytesPerSecond float64
	Timeout        time.Duration
	Opener         Opener
	// Sources are local IPs to spread connections over (e.g. Wi-Fi + USB tether).
	Sources []string

	mu        sync.Mutex
	rotor     int
	thr       *throttle
	stop      chan struct{}
	stopOnce  sync.Once
	segments  []*segment
	maxActive int
	active    int
	errs      []error
}

func (d *Download) stopNow() { d.stopOnce.Do(func() { close(d.stop) }) }

func (d *Download) stopped() bool {
	select {
	case <-d.stop:
		return true
	default:
		return false
	}
}

// -- state -----------------------------------------------------------------

type stateFile struct {
	Total    int64      `json:"total"`
	Segments [][3]int64 `json:"segments"`
}

// loadState restores segments from a previous run, or plans fresh. A partial
// file left by a plain single-connection download counts as its first bytes.
func (d *Download) loadState() {
	var size int64
	exists := false
	if st, err := os.Stat(d.Path); err == nil {
		size, exists = st.Size(), true
	}
	if b, err := os.ReadFile(StatePath(d.Path)); err == nil {
		var sf stateFile
		if json.Unmarshal(b, &sf) == nil && sf.Total == d.Total && size <= d.Total && exists && sf.Segments != nil {
			d.segments = d.segments[:0]
			for _, s := range sf.Segments {
				d.segments = append(d.segments, &segment{s[0], s[1], s[2], -1})
			}
			return
		}
	}
	var done int64
	if size > 0 && size <= d.Total {
		done = size
	}
	if size > d.Total {
		os.Remove(d.Path) // stale/different file; start over
	}
	d.segments = nil
	if d.Total > 0 {
		d.segments = []*segment{{0, d.Total, done, -1}}
	}
}

func (d *Download) saveState() error {
	d.mu.Lock()
	sf := stateFile{Total: d.Total, Segments: make([][3]int64, len(d.segments))}
	for i, s := range d.segments {
		sf.Segments[i] = [3]int64{s.start, s.end, s.pos}
	}
	d.mu.Unlock()
	b, err := json.Marshal(sf)
	if err != nil {
		return err
	}
	tmp := StatePath(d.Path) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, StatePath(d.Path))
}

// Downloaded returns the bytes written so far.
func (d *Download) Downloaded() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.downloadedLocked()
}

func (d *Download) downloadedLocked() int64 {
	var n int64
	for _, s := range d.segments {
		n += s.pos - s.start
	}
	return n
}

// -- planning --------------------------------------------------------------

// split gives the second half of seg's remaining bytes to a new segment, or
// returns nil if what's left is too small. Called with the lock held.
func (d *Download) split(seg *segment) *segment {
	if seg.remaining() < 2*MinSegment {
		return nil
	}
	mid := seg.pos + seg.remaining()/2
	n := &segment{mid, seg.end, mid, -1}
	seg.end = mid
	d.segments = append(d.segments, n)
	return n
}

func largest(segs []*segment, keep func(*segment) bool) *segment {
	var best *segment
	for _, s := range segs {
		if keep(s) && (best == nil || s.remaining() > best.remaining()) {
			best = s
		}
	}
	return best
}

// claim returns the next piece of work for a worker: an unowned unfinished
// segment, or half of the busiest one (work stealing). Lock held.
func (d *Download) claim(worker int) *segment {
	if d.active > d.maxActive {
		return nil
	}
	seg := largest(d.segments, func(s *segment) bool { return s.owner == -1 && s.remaining() > 0 })
	if seg == nil {
		if busy := largest(d.segments, func(s *segment) bool { return s.remaining() > 0 }); busy != nil {
			seg = d.split(busy)
		}
	}
	if seg != nil {
		seg.owner = worker
	}
	return seg
}

// nextSource round-robins over the local IPs: every new request (including a
// retry after a failure) leaves from the next one.
func (d *Download) nextSource() string {
	if len(d.Sources) == 0 {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	src := d.Sources[d.rotor%len(d.Sources)]
	d.rotor++
	return src
}

// -- workers ---------------------------------------------------------------

func (d *Download) fetch(seg *segment, fh *os.File, source string) error {
	d.mu.Lock()
	rng := fmt.Sprintf("bytes=%d-%d", seg.pos, seg.end-1)
	startPos := seg.pos
	d.mu.Unlock()
	r, err := d.Opener(d.URL, copyHeaders(d.Headers, "Range", rng), d.Timeout, source)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	status := r.Status
	if status == 0 {
		status = 200
	}
	m := contentRangeStart.FindStringSubmatch(r.Header("Content-Range"))
	if status != 206 || m == nil || m[1] != strconv.FormatInt(startPos, 10) {
		return &RangeNotSupportedError{fmt.Sprintf("server answered %d to a range request", status)}
	}
	buf := make([]byte, Chunk)
	for !d.stopped() {
		d.mu.Lock()
		want := seg.remaining()
		d.mu.Unlock()
		if want > Chunk {
			want = Chunk
		}
		if want <= 0 {
			return nil // done, or the tail was handed to another worker
		}
		n, rerr := io.ReadAtLeast(r.Body, buf[:want], 1)
		if n == 0 {
			d.mu.Lock()
			left := seg.remaining()
			d.mu.Unlock()
			if left > 0 {
				if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
					return rerr
				}
				return ErrConnectionClosed
			}
			return nil
		}
		d.thr.consume(n, d.stop)
		d.mu.Lock()
		// end may have shrunk meanwhile (split); never write past it.
		w := int64(n)
		if lim := seg.end - seg.pos; w > lim {
			w = lim
		}
		offset := seg.pos
		d.mu.Unlock()
		if w > 0 {
			if _, err := fh.WriteAt(buf[:w], offset); err != nil {
				return err
			}
			d.mu.Lock()
			seg.pos += w
			d.mu.Unlock()
		}
	}
	return nil
}

func (d *Download) worker(id int, wg *sync.WaitGroup) {
	defer wg.Done()
	fh, err := os.OpenFile(d.Path, os.O_RDWR, 0)
	if err != nil {
		d.mu.Lock()
		d.errs = append(d.errs, err)
		d.active--
		d.mu.Unlock()
		return
	}
	defer fh.Close()
	failures := 0
	for !d.stopped() {
		d.mu.Lock()
		seg := d.claim(id)
		if seg == nil {
			d.active--
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
		err := d.fetch(seg, fh, d.nextSource())
		if err != nil {
			code := StatusCode(err)
			var rns *RangeNotSupportedError
			if errors.As(err, &rns) || FatalCodes[code] {
				d.mu.Lock()
				seg.owner = -1
				d.errs = append(d.errs, err)
				d.active--
				d.mu.Unlock()
				d.stopNow()
				return
			}
			d.mu.Lock()
			seg.owner = -1
			d.errs = append(d.errs, err)
			if ThrottleCodes[code] && d.maxActive > 1 && d.active > 1 {
				// The host wants fewer connections: this worker bows out (only
				// while another worker is left to finish its segment).
				d.maxActive--
				d.active--
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
			failures++
			if failures >= MaxWorkerFailures {
				d.mu.Lock()
				d.active--
				d.mu.Unlock()
				return
			}
			backoff := time.Duration(1<<failures) * time.Second
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
			sleepOrStop(backoff, d.stop)
			continue
		}
		failures = 0
		d.mu.Lock()
		if seg.owner == id {
			seg.owner = -1
		}
		d.mu.Unlock()
	}
}

// Run blocks until the file is complete (true) or ShouldStop turns true
// (false, with progress saved for the next run). It returns an error if the
// download can't continue.
func (d *Download) Run() (bool, error) {
	if d.Connections < 1 {
		d.Connections = 1
	}
	if d.ShouldStop == nil {
		d.ShouldStop = func() bool { return false }
	}
	if d.OnProgress == nil {
		d.OnProgress = func(int64) {}
	}
	if d.Timeout <= 0 {
		d.Timeout = 30 * time.Second
	}
	if d.Opener == nil {
		d.Opener = DefaultOpener
	}
	var srcs []string
	for _, s := range d.Sources {
		if s != "" {
			srcs = append(srcs, s)
		}
	}
	d.Sources = srcs
	d.thr = &throttle{rate: d.BytesPerSecond, next: time.Now()}
	d.stop = make(chan struct{})
	d.maxActive = d.Connections

	abs, _ := filepath.Abs(d.Path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return false, err
	}
	d.loadState()
	// Sparse also for a partial file a single-connection run left behind.
	f, err := os.OpenFile(d.Path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return false, err
	}
	makeSparse(f)
	f.Close()

	d.mu.Lock()
	var unfinished int
	var remaining int64
	for _, s := range d.segments {
		if s.remaining() > 0 {
			unfinished++
			remaining += s.remaining()
		}
	}
	d.mu.Unlock()
	if unfinished == 0 {
		return d.finish()
	}

	d.mu.Lock()
	count := unfinished
	if byBytes := int(remaining / MinSegment); byBytes > count {
		count = byBytes
	}
	if count > d.Connections {
		count = d.Connections
	}
	if count < 1 {
		count = 1
	}
	// Split evenly up front, so every connection starts with its own range;
	// work stealing then only balances the tail end.
	for {
		n := 0
		for _, s := range d.segments {
			if s.remaining() > 0 {
				n++
			}
		}
		if n >= count {
			break
		}
		if d.split(largest(d.segments, func(*segment) bool { return true })) == nil {
			break
		}
	}
	d.active = count
	d.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go d.worker(i, &wg)
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	lastSave := time.Now()
	tick := time.NewTicker(250 * time.Millisecond)
loop:
	for {
		if d.ShouldStop() {
			d.stopNow()
		}
		select {
		case <-finished:
			break loop
		case <-tick.C:
		}
		d.OnProgress(d.Downloaded())
		if time.Since(lastSave) > time.Second {
			d.saveState()
			lastSave = time.Now()
		}
	}
	tick.Stop()
	d.OnProgress(d.Downloaded())

	d.mu.Lock()
	left := false
	for _, s := range d.segments {
		if s.remaining() > 0 {
			left = true
		}
	}
	var lastErr error
	if len(d.errs) > 0 {
		lastErr = d.errs[len(d.errs)-1]
	}
	d.mu.Unlock()
	if left {
		d.saveState()
		if d.ShouldStop() {
			return false, nil
		}
		// Every worker gave up: report what went wrong.
		if lastErr == nil {
			lastErr = errors.New("download stopped unexpectedly")
		}
		return false, lastErr
	}
	return d.finish()
}

func (d *Download) finish() (bool, error) {
	os.Remove(StatePath(d.Path))
	st, err := os.Stat(d.Path)
	if err != nil {
		return false, err
	}
	if st.Size() != d.Total {
		return false, fmt.Errorf("downloaded file is %d bytes, expected %d", st.Size(), d.Total)
	}
	return true, nil
}
