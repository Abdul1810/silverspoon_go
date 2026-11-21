package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// AppDir is the directory holding the executable (and the bundled
// playwright-chromium folder). Overridable for development.
var AppDir = func() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}()

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// FindBrowsers returns candidate Chromium executables: the Playwright Chromium
// shipped beside the application first, then Playwright caches, then system
// browsers.
func FindBrowsers() []string {
	exe := "chrome"
	if runtime.GOOS == "windows" {
		exe = "chrome.exe"
	}
	var found []string
	add := func(p string) {
		if p == "" || !isFile(p) {
			return
		}
		for _, f := range found {
			if f == p {
				return
			}
		}
		found = append(found, p)
	}
	add(filepath.Join(AppDir, "playwright-chromium", exe))

	la := os.Getenv("LOCALAPPDATA")
	pf := os.Getenv("PROGRAMFILES")
	pf86 := os.Getenv("PROGRAMFILES(X86)")
	home, _ := os.UserHomeDir()

	type pwRoot struct{ root, sub, exe string }
	roots := []pwRoot{{filepath.Join(la, "ms-playwright"), "chrome-win64", "chrome.exe"}}
	for _, sub := range []string{"chrome-linux64", "chrome-linux-arm64", "chrome-linux"} {
		roots = append(roots, pwRoot{filepath.Join(home, ".cache", "ms-playwright"), sub, "chrome"})
	}
	for _, r := range roots {
		if la == "" && strings.HasSuffix(r.exe, ".exe") {
			continue
		}
		entries, err := os.ReadDir(r.root)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		for _, n := range names {
			if strings.HasPrefix(n, "chromium-") && !strings.HasPrefix(n, "chromium_headless") {
				add(filepath.Join(r.root, n, r.sub, r.exe))
			}
		}
	}

	which := func(name string) string {
		p, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		return p
	}
	join := func(base string, parts ...string) string {
		if base == "" {
			return ""
		}
		return filepath.Join(append([]string{base}, parts...)...)
	}
	for _, p := range []string{
		join(pf, "Google", "Chrome", "Application", "chrome.exe"),
		join(pf86, "Google", "Chrome", "Application", "chrome.exe"),
		join(la, "Google", "Chrome", "Application", "chrome.exe"),
		which("chrome"),
		join(pf, "Microsoft", "Edge", "Application", "msedge.exe"),
		join(pf86, "Microsoft", "Edge", "Application", "msedge.exe"),
		join(la, "Microsoft", "Edge", "Application", "msedge.exe"),
		which("msedge"),
		which("google-chrome"), which("google-chrome-stable"), which("chromium"),
		which("chromium-browser"), which("brave-browser"), which("microsoft-edge"),
		which("microsoft-edge-stable"),
		"/snap/bin/chromium",
	} {
		add(p)
	}
	return found
}

// browserProc is a launched Chromium with a CDP connection to it.
type browserProc struct {
	cmd     *exec.Cmd
	conn    *cdpConn
	exited  chan struct{}
	profile string
}

// launchBrowser starts path headful with the same flags the Python app gives
// nodriver, plus remote debugging, and connects to it.
func launchBrowser(path, profile string, showWindow bool) (*browserProc, error) {
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return nil, err
	}
	// An explicit port, as nodriver uses: with --remote-debugging-port=0 Chrome
	// reports navigator.webdriver = true and Cloudflare blocks the session.
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	pos := "--window-position=-32000,-32000"
	if showWindow {
		pos = "--window-position=50,50"
	}
	args := []string{
		"--remote-debugging-host=127.0.0.1",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-allow-origins=*",
		"--user-data-dir=" + profile,
		"--no-sandbox",
		// nodriver defaults
		"--no-first-run",
		"--no-service-autorun",
		"--no-default-browser-check",
		"--homepage=about:blank",
		"--no-pings",
		"--password-store=basic",
		"--disable-infobars",
		"--disable-breakpad",
		"--disable-dev-shm-usage",
		"--disable-session-crashed-bubble",
		"--disable-search-engine-choice-screen",
		"--disable-features=IsolateOrigins,site-per-process",
		// SilverSpoon's own
		"--disable-restore-session-state",
		"--window-size=500,550",
		pos,
		// Keep timers and rendering at full speed while the window is
		// off-screen or covered by other windows.
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
		"--disable-background-timer-throttling",
	}
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	b := &browserProc{cmd: cmd, exited: make(chan struct{}), profile: profile}
	go func() { cmd.Wait(); close(b.exited) }()

	deadline := time.Now().Add(20 * time.Second)
	var wsURL string
	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	client := &http.Client{Timeout: 2 * time.Second}
	for wsURL == "" && time.Now().Before(deadline) {
		select {
		case <-b.exited:
			return nil, errors.New("browser exited during startup (is another copy using the profile?)")
		default:
		}
		if r, err := client.Get(versionURL); err == nil {
			var v struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(r.Body).Decode(&v)
			r.Body.Close()
			wsURL = v.WebSocketDebuggerURL
		}
		if wsURL == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if wsURL == "" {
		b.kill()
		return nil, errors.New("browser did not open its debugging port")
	}
	conn, err := dialCDP(wsURL)
	if err != nil {
		b.kill()
		return nil, err
	}
	b.conn = conn
	return b, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (b *browserProc) kill() {
	if b.cmd.Process != nil {
		b.cmd.Process.Kill()
	}
	select {
	case <-b.exited:
	case <-time.After(5 * time.Second):
	}
}

// stop asks the browser to close (3 s), then force-kills it.
func (b *browserProc) stop() {
	if b.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		b.conn.call(ctx, "", "Browser.close", nil, nil)
		cancel()
	}
	select {
	case <-b.exited:
	case <-time.After(3 * time.Second):
		Log.Info("TurnstileSolver: browser process force killed")
		b.kill()
	}
	if b.conn != nil {
		b.conn.close()
	}
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
}

func (b *browserProc) targets(ctx context.Context) ([]targetInfo, error) {
	var res struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	err := b.conn.call(ctx, "", "Target.getTargets", nil, &res)
	return res.TargetInfos, err
}
