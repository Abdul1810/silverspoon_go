package transport

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"silverspoon/internal/segmented"
)

func TestParseSources(t *testing.T) {
	got := ParseSources(" 192.168.1.9, 172.16.60.60 ,,")
	if !reflect.DeepEqual(got, []string{"192.168.1.9", "172.16.60.60"}) {
		t.Errorf("%v", got)
	}
	if ParseSources("") != nil {
		t.Error("empty")
	}
}

func TestLocalIPv4AddressesSkipsLoopback(t *testing.T) {
	for _, ip := range LocalIPv4Addresses() {
		if ip[:4] == "127." {
			t.Errorf("loopback %s", ip)
		}
	}
}

// TestChromeOpenerSegmented drives a segmented download through the
// fingerprinted transport, checking cookies and headers reach the server.
func TestChromeOpenerSegmented(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<20) // 16 MiB
	var sawCookie, sawUA bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("cf_clearance"); err == nil && c.Value == "tok" {
			sawCookie = true
		}
		if r.Header.Get("User-Agent") == "SolverUA/1.0" {
			sawUA = true
		}
		http.ServeContent(w, r, "f.bin", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	opener := ChromeOpener(map[string]string{"cf_clearance": "tok"})
	h := map[string]string{"User-Agent": "SolverUA/1.0"}
	total, ok, err := segmented.Probe(srv.URL, h, 0, opener)
	if err != nil || !ok || total != int64(len(data)) {
		t.Fatalf("probe %d %v %v", total, ok, err)
	}
	path := filepath.Join(t.TempDir(), "f.bin")
	done, err := (&segmented.Download{URL: srv.URL, Path: path, Total: total, Connections: 4,
		Headers: h, Opener: opener, Sources: []string{"127.0.0.1"}}).Run()
	if !done || err != nil {
		t.Fatalf("%v %v", done, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, data) || !sawCookie || !sawUA {
		t.Errorf("equal=%v cookie=%v ua=%v", bytes.Equal(got, data), sawCookie, sawUA)
	}
	// Statuses >= 400 surface as HTTPStatusError.
	srv404 := httptest.NewServer(http.NotFoundHandler())
	defer srv404.Close()
	if _, err := opener(srv404.URL, nil, 0, ""); segmented.StatusCode(err) != 404 {
		t.Errorf("err = %v", err)
	}
	r, err := ChromeGet(t.Context(), srv.URL, h, nil, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if n != int64(len(data)) {
		t.Errorf("streamed %d", n)
	}
}
