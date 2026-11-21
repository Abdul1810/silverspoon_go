package netmon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"silverspoon/internal/segmented"
)

// TestLiveMultiNetwork downloads a small public file over every network
// with internet: SILVERSPOON_LIVE_NET=1 go test -run LiveMulti ./internal/netmon
func TestLiveMultiNetwork(t *testing.T) {
	if os.Getenv("SILVERSPOON_LIVE_NET") == "" {
		t.Skip("set SILVERSPOON_LIVE_NET=1")
	}
	m := New()
	m.Scan()
	srcs := m.Usable()
	t.Logf("usable networks: %v", srcs)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
				m.tick()
			}
		}
	}()
	url := "https://proof.ovh.net/files/100Mb.dat"
	total, ranges, err := segmented.Probe(url, nil, 0, nil)
	if err != nil || !ranges {
		t.Skipf("test file unavailable: %v %v", ranges, err)
	}
	path := filepath.Join(t.TempDir(), "f.dat")
	t0 := time.Now()
	ok, err := (&segmented.Download{URL: url, Path: path, Total: total, Connections: 4,
		SourcesFunc: m.Usable, Meter: m}).Run()
	close(stop)
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	t.Logf("%.1f MB in %v", float64(total)/(1<<20), time.Since(t0).Round(time.Millisecond))
	for _, n := range m.Snapshot() {
		t.Logf("  %-12s %-15s %-8s %6.1f MB", n.Name, n.IP, n.Kind, float64(n.TotalBytes)/(1<<20))
		if n.Online && n.TotalBytes == 0 {
			t.Errorf("%s carried nothing", n.Name)
		}
	}
}
