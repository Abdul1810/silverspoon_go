// Package netmon finds the network connections (Ethernet, Wi-Fi, USB or
// phone tethering, ...) that can reach the internet, notices when one is
// plugged in or drops out, and measures how much each one is downloading.
//
// Downloads bind each connection to a local IP (see segmented.Download), so
// counting bytes per local IP gives a per-network bandwidth figure.
package netmon

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is the type of network an adapter is.
type Kind string

const (
	KindEthernet Kind = "ethernet"
	KindWiFi     Kind = "wifi"
	KindTether   Kind = "tether" // USB/phone tethering (RNDIS, iPhone USB, ...)
	KindCellular Kind = "cellular"
	KindOther    Kind = "other"
)

// Adapter is one network interface with an IPv4 address and a gateway.
type Adapter struct {
	Name        string // friendly name, e.g. "Ethernet 3", "Wi-Fi"
	Description string // driver description, e.g. "Remote NDIS based Internet Sharing Device"
	IP          string
	Gateway     string
	Kind        Kind
	LinkMbps    uint64 // reported link speed, 0 when unknown
}

// State is how a network is doing.
type State int

const (
	// StateOnline: the last internet check passed.
	StateOnline State = iota
	// StateReconnecting: one check failed, or the adapter vanished for a
	// moment (a USB tether resetting). It keeps its connections while its
	// address still exists.
	StateReconnecting
	// StateOffline: failed checks in a row, or gone for longer than the
	// grace period.
	StateOffline
)

// FailuresBeforeOffline is how many internet checks in a row must fail
// before a network is treated as having no internet.
const FailuresBeforeOffline = 2

// MissingGrace is how long an adapter that disappeared (e.g. a tether
// resetting) is still listed as reconnecting.
const MissingGrace = 30 * time.Second

// Net is an adapter plus live statistics, as the UI shows it.
type Net struct {
	Adapter
	State       State
	Online      bool    // usable for downloads (online, or reconnecting with its address present)
	Missing     bool    // the adapter is briefly gone (its address can't be used)
	Excluded    bool    // the user turned it off
	Connections int     // open download connections leaving from it
	BytesPerSec float64 // smoothed download rate
	TotalBytes  int64   // downloaded through it since start
	Default     bool    // the OS's default route
}

type counter struct {
	conns   int
	total   int64
	window  int64 // bytes since the last rate tick
	rate    float64
	lastAdd time.Time
}

// Monitor tracks adapters and per-IP traffic.
type Monitor struct {
	mu        sync.Mutex
	adapters  []Adapter
	fails     map[string]int       // consecutive failed checks, by adapter name
	lastSeen  map[string]time.Time // by adapter name
	known     map[string]Adapter   // last details of every adapter seen, by name
	counters  map[string]*counter
	excluded  map[string]bool // by adapter name
	defaultIP string
	lastTick  time.Time

	// Hooks for tests.
	list  func() []Adapter
	probe func(ip string) bool
	now   func() time.Time
}

// New returns a monitor using the OS adapter list.
func New() *Monitor {
	return &Monitor{fails: map[string]int{}, lastSeen: map[string]time.Time{}, known: map[string]Adapter{},
		counters: map[string]*counter{}, excluded: map[string]bool{},
		list: listAdapters, probe: probeInternet, now: time.Now}
}

// SetExcluded sets the adapter names the user turned off.
func (m *Monitor) SetExcluded(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.excluded = map[string]bool{}
	for _, n := range names {
		m.excluded[n] = true
	}
}

// Excluded returns the names the user turned off.
func (m *Monitor) Excluded() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.excluded {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Run rescans adapters every scanEvery and updates rates once a second
// until ctx ends. It scans once before returning control to the loop, so
// callers can rely on Snapshot shortly after start.
func (m *Monitor) Run(ctx context.Context, scanEvery time.Duration) {
	m.Scan()
	scan := time.NewTicker(scanEvery)
	rate := time.NewTicker(time.Second)
	defer scan.Stop()
	defer rate.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			m.Scan()
		case <-rate.C:
			m.tick()
		}
	}
}

// Scan re-reads the adapters and checks which reach the internet. A newly
// plugged-in tether shows up here and is used from the next request on. A
// single failed check, or an adapter vanishing for a moment, only marks the
// network as reconnecting; see State.
func (m *Monitor) Scan() {
	ads := m.list()
	ok := map[string]bool{}
	var wg sync.WaitGroup
	var omu sync.Mutex
	for _, a := range ads {
		wg.Add(1)
		go func(name, ip string) {
			defer wg.Done()
			r := m.probe(ip)
			omu.Lock()
			ok[name] = r
			omu.Unlock()
		}(a.Name, a.IP)
	}
	wg.Wait()
	def := defaultIP()
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	present := map[string]bool{}
	for _, a := range ads {
		present[a.Name] = true
		m.known[a.Name] = a
		m.lastSeen[a.Name] = now
		if ok[a.Name] {
			m.fails[a.Name] = 0
		} else {
			m.fails[a.Name]++
		}
	}
	// Adapters that disappeared: keep them as reconnecting for a while, then
	// forget them.
	for name := range m.known {
		if present[name] {
			continue
		}
		m.fails[name]++
		if now.Sub(m.lastSeen[name]) > MissingGrace {
			delete(m.known, name)
			delete(m.fails, name)
			delete(m.lastSeen, name)
		}
	}
	m.adapters, m.defaultIP = ads, def
}

// stateOf is the state of a known adapter. Lock held.
func (m *Monitor) stateOf(name string, present bool) State {
	switch {
	case present && m.fails[name] == 0:
		return StateOnline
	case !present && m.now().Sub(m.lastSeen[name]) <= MissingGrace:
		return StateReconnecting
	case present && m.fails[name] < FailuresBeforeOffline:
		return StateReconnecting
	}
	return StateOffline
}

func (m *Monitor) tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	dt := now.Sub(m.lastTick).Seconds()
	if m.lastTick.IsZero() || dt <= 0 {
		dt = 1
	}
	m.lastTick = now
	for _, c := range m.counters {
		inst := float64(c.window) / dt
		// Exponential smoothing so the figure doesn't jump around.
		c.rate = c.rate*0.5 + inst*0.5
		if c.rate < 1 && c.window == 0 {
			c.rate = 0
		}
		c.window = 0
	}
}

func (m *Monitor) counterFor(ip string) *counter {
	if ip == "" {
		ip = m.defaultIP
	}
	c := m.counters[ip]
	if c == nil {
		c = &counter{}
		m.counters[ip] = c
	}
	return c
}

// Add records n bytes received through local IP ip ("" = OS default).
func (m *Monitor) Add(ip string, n int) {
	m.mu.Lock()
	c := m.counterFor(ip)
	c.total += int64(n)
	c.window += int64(n)
	c.lastAdd = time.Now()
	m.mu.Unlock()
}

// Open records a connection opened from ip; Close records it ending.
func (m *Monitor) Open(ip string) {
	m.mu.Lock()
	m.counterFor(ip).conns++
	m.mu.Unlock()
}

// Close records a connection from ip ending.
func (m *Monitor) Close(ip string) {
	m.mu.Lock()
	if c := m.counterFor(ip); c.conns > 0 {
		c.conns--
	}
	m.mu.Unlock()
}

// Usable returns the IPs downloads should spread over: online, not
// excluded. Sorted by name so the assignment of connections is stable.
func (m *Monitor) Usable() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ads := append([]Adapter(nil), m.adapters...)
	sort.Slice(ads, func(i, j int) bool { return ads[i].Name < ads[j].Name })
	var out []string
	for _, a := range ads {
		// A network that failed one check keeps its connections; only after
		// FailuresBeforeOffline failures in a row do they move elsewhere.
		if m.stateOf(a.Name, true) != StateOffline && !m.excluded[a.Name] {
			out = append(out, a.IP)
		}
	}
	return out
}

// Present reports whether ip belongs to a current adapter.
func (m *Monitor) Present(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.adapters {
		if a.IP == ip {
			return true
		}
	}
	return false
}

// Snapshot returns every adapter with its live figures, busiest first.
func (m *Monitor) Snapshot() []Net {
	m.mu.Lock()
	defer m.mu.Unlock()
	present := map[string]bool{}
	out := make([]Net, 0, len(m.known))
	add := func(a Adapter, isPresent bool) {
		st := m.stateOf(a.Name, isPresent)
		n := Net{Adapter: a, State: st, Missing: !isPresent, Excluded: m.excluded[a.Name],
			Online: isPresent && st != StateOffline, Default: isPresent && a.IP == m.defaultIP}
		if c := m.counters[a.IP]; c != nil {
			n.Connections, n.BytesPerSec, n.TotalBytes = c.conns, c.rate, c.total
		}
		out = append(out, n)
	}
	for _, a := range m.adapters {
		present[a.Name] = true
		add(a, true)
	}
	for name, a := range m.known {
		if !present[name] && m.stateOf(name, false) == StateReconnecting {
			add(a, false)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// kindFor guesses the network type from the adapter's names.
func kindFor(name, desc string, wifi bool) Kind {
	d := strings.ToLower(name + " " + desc)
	switch {
	case wifi || strings.Contains(d, "wi-fi") || strings.Contains(d, "wireless") || strings.Contains(d, "wlan") || strings.Contains(d, "802.11"):
		return KindWiFi
	case strings.Contains(d, "ndis") || strings.Contains(d, "tether") || strings.Contains(d, "apple mobile") ||
		strings.Contains(d, "iphone") || strings.Contains(d, "android") || strings.Contains(d, "usb") ||
		strings.HasPrefix(name, "usb") || strings.HasPrefix(name, "enx") || strings.HasPrefix(name, "rndis"):
		return KindTether
	case strings.Contains(d, "mobile broadband") || strings.Contains(d, "cellular") || strings.HasPrefix(name, "wwan"):
		return KindCellular
	case strings.Contains(d, "ethernet") || strings.Contains(d, "gbe") || strings.Contains(d, "realtek") ||
		strings.HasPrefix(name, "eth") || strings.HasPrefix(name, "en"):
		return KindEthernet
	}
	return KindOther
}

// isVirtual filters out adapters that never carry internet traffic of
// their own (VPN-less virtual switches, VMs, WSL).
func isVirtual(name, desc string) bool {
	d := strings.ToLower(name + " " + desc)
	for _, s := range []string{"vethernet", "hyper-v", "virtualbox", "vmware", "wsl", "loopback", "docker", "vbox", "tap-windows"} {
		if strings.Contains(d, s) {
			return true
		}
	}
	return false
}

// probeInternet opens a TCP connection to 1.1.1.1:443 from ip.
func probeInternet(ip string) bool {
	d := net.Dialer{Timeout: 2 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}}
	c, err := d.Dial("tcp4", "1.1.1.1:443")
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// defaultIP is the local address the OS picks for internet traffic (no
// packet is sent for a UDP "connect").
func defaultIP() string {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}
