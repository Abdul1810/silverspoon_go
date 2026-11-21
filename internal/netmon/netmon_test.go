package netmon

import (
	"reflect"
	"testing"
	"time"
)

func fake(ads []Adapter, online map[string]bool) *Monitor {
	m := New()
	m.list = func() []Adapter { return ads }
	m.probe = func(ip string) bool { return online[ip] }
	return m
}

func TestUsableFollowsHotplugAndExclusions(t *testing.T) {
	eth := Adapter{Name: "Ethernet", IP: "192.168.1.9", Kind: KindEthernet}
	tether := Adapter{Name: "Ethernet 3", IP: "172.16.60.60", Kind: KindTether}
	ads := []Adapter{eth}
	online := map[string]bool{"192.168.1.9": true, "172.16.60.60": true}
	m := New()
	m.list = func() []Adapter { return ads }
	m.probe = func(ip string) bool { return online[ip] }

	m.Scan()
	if got := m.Usable(); !reflect.DeepEqual(got, []string{"192.168.1.9"}) {
		t.Fatalf("one network: %v", got)
	}
	// A phone is tethered mid-download.
	ads = []Adapter{eth, tether}
	m.Scan()
	if got := m.Usable(); !reflect.DeepEqual(got, []string{"192.168.1.9", "172.16.60.60"}) {
		t.Fatalf("after tether: %v", got)
	}
	// The user turns Ethernet off in the UI.
	m.SetExcluded([]string{"Ethernet"})
	if got := m.Usable(); !reflect.DeepEqual(got, []string{"172.16.60.60"}) {
		t.Fatalf("excluded: %v", got)
	}
	m.SetExcluded(nil)
	// The tether loses internet (still plugged in): one failed check keeps
	// it (reconnecting), the second moves its connections away.
	online["172.16.60.60"] = false
	m.Scan()
	if got := m.Usable(); len(got) != 2 {
		t.Fatalf("one failed check should keep the tether: %v", got)
	}
	m.Scan()
	if got := m.Usable(); !reflect.DeepEqual(got, []string{"192.168.1.9"}) {
		t.Fatalf("tether offline: %v", got)
	}
	if !m.Present("172.16.60.60") || m.Present("172.16.60.36") {
		t.Error("Present")
	}
}

func TestCountersAndRates(t *testing.T) {
	m := fake([]Adapter{{Name: "A", IP: "10.0.0.2"}, {Name: "B", IP: "10.0.1.2"}}, map[string]bool{"10.0.0.2": true, "10.0.1.2": true})
	m.Scan()
	m.Open("10.0.0.2")
	m.Open("10.0.0.2")
	m.Open("10.0.1.2")
	m.Add("10.0.0.2", 3_000_000)
	m.Add("10.0.1.2", 1_000_000)
	m.tick()
	m.Close("10.0.0.2")
	snap := map[string]Net{}
	for _, n := range m.Snapshot() {
		snap[n.Name] = n
	}
	if snap["A"].Connections != 1 || snap["B"].Connections != 1 {
		t.Errorf("connections %+v", snap)
	}
	if snap["A"].TotalBytes != 3_000_000 || snap["A"].BytesPerSec <= snap["B"].BytesPerSec {
		t.Errorf("rates %+v", snap)
	}
}

func TestKindFor(t *testing.T) {
	for _, c := range []struct {
		name, desc string
		want       Kind
	}{
		{"Ethernet 3", "Remote NDIS based Internet Sharing Device", KindTether},
		{"Ethernet 4", "Apple Mobile Device Ethernet", KindTether},
		{"Wi-Fi", "Intel(R) Wi-Fi 6 AX201 160MHz", KindWiFi},
		{"Ethernet", "Realtek PCIe GbE Family Controller", KindEthernet},
		{"usb0", "", KindTether},
		{"wlan0", "", KindWiFi},
	} {
		if got := kindFor(c.name, c.desc, false); got != c.want {
			t.Errorf("%s/%s = %s", c.name, c.desc, got)
		}
	}
	if !isVirtual("vEthernet (WSL)", "Hyper-V Virtual Ethernet Adapter") || isVirtual("Ethernet", "Realtek") {
		t.Error("isVirtual")
	}
}

// TestRealAdapters lists this machine's networks (informational).
func TestRealAdapters(t *testing.T) {
	m := New()
	m.Scan()
	for _, n := range m.Snapshot() {
		t.Logf("%-14s %-15s %-8s online=%v default=%v  %s", n.Name, n.IP, n.Kind, n.Online, n.Default, n.Description)
	}
}

func stateOf(t *testing.T, m *Monitor, name string) Net {
	t.Helper()
	for _, n := range m.Snapshot() {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("%s not listed", name)
	return Net{}
}

// TestReconnectingStates walks a USB tether through a reset: a failed check,
// the adapter vanishing for a few seconds, coming back, and finally staying
// away past the grace period.
func TestReconnectingStates(t *testing.T) {
	eth := Adapter{Name: "Home", IP: "192.168.1.9"}
	tether := Adapter{Name: "Ethernet 3", IP: "172.16.60.139", Kind: KindTether}
	ads := []Adapter{eth, tether}
	online := map[string]bool{"192.168.1.9": true, "172.16.60.139": true}
	clock := time.Date(2026, 11, 21, 16, 0, 0, 0, time.UTC)
	m := New()
	m.list = func() []Adapter { return ads }
	m.probe = func(ip string) bool { return online[ip] }
	m.now = func() time.Time { return clock }

	m.Scan()
	if n := stateOf(t, m, "Ethernet 3"); n.State != StateOnline || !n.Online {
		t.Fatalf("start: %+v", n)
	}
	// One failed check: reconnecting, still usable.
	online["172.16.60.139"] = false
	m.Scan()
	if n := stateOf(t, m, "Ethernet 3"); n.State != StateReconnecting || !n.Online {
		t.Fatalf("one failure: %+v", n)
	}
	// Check passes again: back online.
	online["172.16.60.139"] = true
	m.Scan()
	if n := stateOf(t, m, "Ethernet 3"); n.State != StateOnline {
		t.Fatalf("recovered: %+v", n)
	}
	// The adapter resets and disappears: still listed as reconnecting, but
	// its address can't be used, so it isn't offered to downloads.
	ads = []Adapter{eth}
	clock = clock.Add(4 * time.Second)
	m.Scan()
	n := stateOf(t, m, "Ethernet 3")
	if n.State != StateReconnecting || !n.Missing || n.Online {
		t.Fatalf("resetting: %+v", n)
	}
	if got := m.Usable(); !reflect.DeepEqual(got, []string{"192.168.1.9"}) {
		t.Fatalf("missing adapter offered: %v", got)
	}
	// It comes back within the grace period: online again.
	ads = []Adapter{eth, tether}
	clock = clock.Add(8 * time.Second)
	m.Scan()
	if n := stateOf(t, m, "Ethernet 3"); n.State != StateOnline || n.Missing {
		t.Fatalf("back: %+v", n)
	}
	// Unplugged for good: after the grace period it leaves the list.
	ads = []Adapter{eth}
	clock = clock.Add(4 * time.Second)
	m.Scan()
	clock = clock.Add(MissingGrace + time.Second)
	m.Scan()
	for _, n := range m.Snapshot() {
		if n.Name == "Ethernet 3" {
			t.Fatalf("still listed after grace: %+v", n)
		}
	}
}
