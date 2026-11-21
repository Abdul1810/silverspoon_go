package segmented

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var data = func() []byte {
	b := make([]byte, 16*1024*1024)
	rand.Read(b)
	return b
}()

type stats struct {
	mu                             sync.Mutex
	served, active, peak, rejected int64
}

func (s *stats) get() (served, peak, rejected int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served, s.peak, s.rejected
}

type serveOpts struct {
	perConnRate float64 // bytes/s per connection, 0 = unlimited
	maxConns    int64
	ignoreRange bool
	status      int
}

// serve starts a server for data that supports Range and can be made slow per
// connection, throttling (503 above N connections), range-ignoring or 404.
func serve(o serveOpts) (string, *stats, func()) {
	st := &stats{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o.status != 0 {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(o.status)
			return
		}
		st.mu.Lock()
		reject := o.maxConns > 0 && st.active >= o.maxConns
		if reject {
			st.rejected++
		} else {
			st.active++
			if st.active > st.peak {
				st.peak = st.active
			}
		}
		st.mu.Unlock()
		if reject {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(503)
			return
		}
		defer func() {
			st.mu.Lock()
			st.active--
			st.mu.Unlock()
		}()
		start, end := 0, len(data)-1
		status := 200
		if rng := r.Header.Get("Range"); rng != "" && !o.ignoreRange {
			a, b, _ := strings.Cut(strings.SplitN(rng, "=", 2)[1], "-")
			start, _ = strconv.Atoi(a)
			if b != "" {
				end, _ = strconv.Atoi(b)
			}
			status = 206
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		}
		body := data[start : end+1]
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(status)
		const step = 64 * 1024
		for i := 0; i < len(body); i += step {
			part := body[i:min(i+step, len(body))]
			if _, err := w.Write(part); err != nil {
				return // client closed early (pause/split): fine
			}
			st.mu.Lock()
			st.served += int64(len(part))
			st.mu.Unlock()
			if o.perConnRate > 0 {
				time.Sleep(time.Duration(float64(len(part)) / o.perConnRate * float64(time.Second)))
			}
		}
	}))
	return srv.URL + "/file.bin", st, func() {
		srv.CloseClientConnections()
		srv.Close()
	}
}

func readFile(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestProbe(t *testing.T) {
	url, _, stop := serve(serveOpts{})
	n, ok, err := Probe(url, nil, 0, nil)
	stop()
	if err != nil || n != int64(len(data)) || !ok {
		t.Errorf("range server: %d %v %v", n, ok, err)
	}
	url, _, stop = serve(serveOpts{ignoreRange: true})
	n, ok, err = Probe(url, nil, 0, nil)
	stop()
	if err != nil || n != int64(len(data)) || ok {
		t.Errorf("ignoring server: %d %v %v", n, ok, err)
	}
}

func TestParallelIsFasterAndIdentical(t *testing.T) {
	d := t.TempDir()
	times := map[int]time.Duration{}
	for _, conns := range []int{1, 4} {
		url, st, stop := serve(serveOpts{perConnRate: 4 * 1024 * 1024})
		path := filepath.Join(d, fmt.Sprintf("f%d.bin", conns))
		t0 := time.Now()
		ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: conns}).Run()
		stop()
		times[conns] = time.Since(t0)
		if !ok || err != nil {
			t.Fatalf("conns %d: %v %v", conns, ok, err)
		}
		if !bytes.Equal(readFile(t, path), data) {
			t.Fatalf("conns %d: content differs", conns)
		}
		if exists(StatePath(path)) {
			t.Error("state file left behind")
		}
		_, peak, _ := st.get()
		if peak > int64(conns+1) || (conns == 4 && peak < 3) {
			t.Errorf("conns %d: peak %d", conns, peak)
		}
	}
	if times[4] > time.Duration(float64(times[1])*0.6) {
		t.Errorf("not faster: %v", times)
	}
	t.Logf("1 connection %v, 4 connections %v", times[1], times[4])
}

func TestPauseAndResume(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "f.bin")
	url, st, stop := serve(serveOpts{perConnRate: 4 * 1024 * 1024})
	defer stop()
	var mu sync.Mutex
	var seen []int64
	last := func() int64 {
		mu.Lock()
		defer mu.Unlock()
		if len(seen) == 0 {
			return 0
		}
		return seen[len(seen)-1]
	}
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4,
		ShouldStop: func() bool { return last() > 6*1024*1024 },
		OnProgress: func(n int64) { mu.Lock(); seen = append(seen, n); mu.Unlock() }}).Run()
	if ok || err != nil {
		t.Fatalf("pause: %v %v", ok, err)
	}
	if !exists(StatePath(path)) {
		t.Fatal("no state file after pause")
	}
	pausedAt := last()
	if pausedAt <= 0 || pausedAt >= int64(len(data)) {
		t.Fatalf("paused at %d", pausedAt)
	}
	servedBefore, _, _ := st.get()
	ok, err = (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4}).Run()
	if !ok || err != nil {
		t.Fatalf("resume: %v %v", ok, err)
	}
	if !bytes.Equal(readFile(t, path), data) || exists(StatePath(path)) {
		t.Fatal("bad result after resume")
	}
	served, _, _ := st.get()
	resumed := served - servedBefore
	if limit := int64(len(data)) - pausedAt + 2*1024*1024; resumed > limit {
		t.Errorf("resumed %d > %d", resumed, limit)
	}
}

func TestResumeFromSingleConnectionPartial(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "f.bin")
	os.WriteFile(path, data[:5*1024*1024], 0o644)
	url, st, stop := serve(serveOpts{})
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4}).Run()
	stop()
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if !bytes.Equal(readFile(t, path), data) {
		t.Fatal("content differs")
	}
	served, _, _ := st.get()
	if served > int64(len(data))-5*1024*1024+2*1024*1024 {
		t.Errorf("served %d", served)
	}
}

func TestThrottlingHostGetsFewerConnections(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "f.bin")
	url, st, stop := serve(serveOpts{perConnRate: 8 * 1024 * 1024, maxConns: 2})
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 6}).Run()
	stop()
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if !bytes.Equal(readFile(t, path), data) {
		t.Fatal("content differs")
	}
	_, peak, rejected := st.get()
	if rejected == 0 || peak > 2 {
		t.Errorf("rejected %d peak %d", rejected, peak)
	}
}

func TestRangeIgnoredAndDeadLinksFailFast(t *testing.T) {
	d := t.TempDir()
	for _, o := range []serveOpts{{ignoreRange: true}, {status: 404}} {
		url, _, stop := serve(o)
		t0 := time.Now()
		_, err := (&Download{URL: url, Path: filepath.Join(d, "f.bin"), Total: int64(len(data)), Connections: 4}).Run()
		stop()
		if err == nil {
			t.Fatalf("%+v: no error", o)
		}
		if o.ignoreRange {
			if _, ok := err.(*RangeNotSupportedError); !ok {
				t.Errorf("want RangeNotSupportedError, got %v", err)
			}
		} else if StatusCode(err) != 404 {
			t.Errorf("want HTTP 404, got %v", err)
		}
		if el := time.Since(t0); el > 5*time.Second {
			t.Errorf("%+v took %v", o, el)
		}
	}
}

func TestBandwidthLimit(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "f.bin")
	url, _, stop := serve(serveOpts{})
	t0 := time.Now()
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4,
		BytesPerSecond: 8 * 1024 * 1024}).Run()
	stop()
	if el := time.Since(t0); el < 1600*time.Millisecond {
		t.Errorf("too fast: %v", el)
	}
	if !ok || err != nil || !bytes.Equal(readFile(t, path), data) {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestCustomOpenerIsUsedForProbeAndRanges(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	opener := func(url string, h map[string]string, timeout time.Duration, source string) (*Response, error) {
		mu.Lock()
		calls = append(calls, h["Range"])
		mu.Unlock()
		return DefaultOpener(url, copyHeaders(h, "X-Test", "1"), timeout, source)
	}
	d := t.TempDir()
	path := filepath.Join(d, "f.bin")
	url, _, stop := serve(serveOpts{})
	defer stop()
	n, okr, err := Probe(url, nil, 0, opener)
	if err != nil || n != int64(len(data)) || !okr {
		t.Fatalf("probe %d %v %v", n, okr, err)
	}
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4, Opener: opener}).Run()
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if len(calls) < 5 || calls[0] != "bytes=0-0" {
		t.Errorf("calls %v", calls)
	}
	if !bytes.Equal(readFile(t, path), data) {
		t.Fatal("content differs")
	}
}

func TestConnectionsBindToGivenSources(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	opener := func(url string, h map[string]string, timeout time.Duration, source string) (*Response, error) {
		mu.Lock()
		seen[source] = true
		mu.Unlock()
		return DefaultOpener(url, h, timeout, source)
	}
	d := t.TempDir()
	path := filepath.Join(d, "f.bin")
	url, _, stop := serve(serveOpts{})
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4, Opener: opener,
		Sources: []string{"127.0.0.1", "127.0.0.2"}}).Run()
	stop()
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if len(seen) != 2 || !seen["127.0.0.1"] || !seen["127.0.0.2"] {
		t.Errorf("sources %v", seen)
	}
	if !bytes.Equal(readFile(t, path), data) {
		t.Fatal("content differs")
	}
}

type meter struct {
	mu    sync.Mutex
	bytes map[string]int64
	conns map[string]int
	peak  map[string]int
}

func (m *meter) Add(s string, n int) { m.mu.Lock(); m.bytes[s] += int64(n); m.mu.Unlock() }
func (m *meter) Open(s string) {
	m.mu.Lock()
	m.conns[s]++
	if m.conns[s] > m.peak[s] {
		m.peak[s] = m.conns[s]
	}
	m.mu.Unlock()
}
func (m *meter) Close(s string) { m.mu.Lock(); m.conns[s]--; m.mu.Unlock() }

// TestNetworkPluggedInMidDownload starts on one source and adds a second
// partway through: connections move over without losing bytes, and the
// meter sees traffic on both.
func TestNetworkPluggedInMidDownload(t *testing.T) {
	url, _, stop := serve(serveOpts{perConnRate: 1024 * 1024})
	defer stop()
	var mu sync.Mutex
	srcs := []string{"127.0.0.1"}
	m := &meter{bytes: map[string]int64{}, conns: map[string]int{}, peak: map[string]int{}}
	path := filepath.Join(t.TempDir(), "f.bin")
	go func() {
		time.Sleep(1500 * time.Millisecond)
		mu.Lock()
		srcs = []string{"127.0.0.1", "127.0.0.2"}
		mu.Unlock()
	}()
	ok, err := (&Download{URL: url, Path: path, Total: int64(len(data)), Connections: 4, Meter: m,
		SourcesFunc: func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), srcs...) }}).Run()
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if !bytes.Equal(readFile(t, path), data) {
		t.Fatal("content differs")
	}
	if m.bytes["127.0.0.2"] == 0 || m.peak["127.0.0.2"] == 0 {
		t.Errorf("new network unused: %v %v", m.bytes, m.peak)
	}
	if m.bytes["127.0.0.1"]+m.bytes["127.0.0.2"] < int64(len(data)) {
		t.Errorf("meter missed bytes: %v", m.bytes)
	}
	for s, c := range m.conns {
		if c != 0 {
			t.Errorf("%s left %d open", s, c)
		}
	}
	t.Logf("bytes per network: %v, peak connections: %v", m.bytes, m.peak)
}
