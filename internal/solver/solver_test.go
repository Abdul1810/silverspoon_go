package solver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBrowserPlumbing launches a real browser (skipped when none is found)
// and checks the CDP pieces the host flows rely on: navigation, evaluation,
// the popup blocker, window moves, network events and cookies.
func TestBrowserPlumbing(t *testing.T) {
	if testing.Short() || len(FindBrowsers()) == 0 {
		t.Skip("no browser")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "cf_clearance", Value: "abc", Path: "/"})
		fmt.Fprint(w, `<html><body><a id="x" href="/other" target="_blank">x</a></body></html>`)
	}))
	defer srv.Close()

	s := New(t.TempDir(), 10, false)
	s.mu.Lock()
	defer func() { s.closeBrowser(); s.mu.Unlock() }()
	if err := s.ensureBrowser(); err != nil {
		t.Fatal(err)
	}
	tb := s.tab

	responses := make(chan string, 10)
	unsub := s.b.conn.subscribe(func(method, session string, params json.RawMessage) {
		if method == "Network.responseReceived" && session == tb.sessionID {
			var ev struct {
				Response struct{ URL string } `json:"response"`
			}
			json.Unmarshal(params, &ev)
			select {
			case responses <- ev.Response.URL:
			default:
			}
		}
	})
	defer unsub()
	ctx, cancel := withTimeout(5 * time.Second)
	if err := tb.call(ctx, "Network.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	cancel()

	if err := tb.navigate(srv.URL+"/page", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	raw, err := tb.evaluate(`JSON.stringify({href: location.href, open: window.open('about:blank'), target: document.getElementById('x').target, wd: navigator.webdriver})`, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Href   string  `json:"href"`
		Open   *string `json:"open"`
		Target string  `json:"target"`
		WD     bool    `json:"wd"`
	}
	json.Unmarshal([]byte(raw), &st)
	if st.Href != srv.URL+"/page" || st.Open != nil || st.Target != "_self" || st.WD {
		t.Errorf("page state %s", raw)
	}
	raw, err = tb.evaluate(`(async () => JSON.stringify((await fetch('/x')).status))()`, true, 5*time.Second)
	if err != nil || raw != "200" {
		t.Errorf("await fetch: %q %v", raw, err)
	}
	select {
	case u := <-responses:
		if u == "" {
			t.Error("empty response url")
		}
	case <-time.After(5 * time.Second):
		t.Error("no Network.responseReceived event")
	}
	if err := tb.setWindow(50, 50, 500, 550); err != nil {
		t.Errorf("show window: %v", err)
	}
	if err := tb.setWindow(-32000, -32000, 500, 550); err != nil {
		t.Errorf("hide window: %v", err)
	}
	ctx, cancel = withTimeout(5 * time.Second)
	defer cancel()
	var cres struct {
		Cookies []struct{ Name, Value string } `json:"cookies"`
	}
	if err := s.b.conn.call(ctx, "", "Storage.getCookies", nil, &cres); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cres.Cookies {
		if c.Name == "cf_clearance" && c.Value == "abc" {
			found = true
		}
	}
	if !found {
		t.Errorf("cookies %+v", cres.Cookies)
	}
	if _, err := tb.evaluate("throw new Error('boom')", false, 5*time.Second); err == nil {
		t.Error("exception not reported")
	}
}

// TestLiveResolve resolves a real link: SILVERSPOON_LIVE_LINK=<url> go test -run Live ./internal/solver/
func TestLiveResolve(t *testing.T) {
	link := os.Getenv("SILVERSPOON_LIVE_LINK")
	if link == "" {
		t.Skip("set SILVERSPOON_LIVE_LINK")
	}
	s := New(t.TempDir(), 10, false)
	defer s.Stop()
	res, err := s.GetDirectLink(link, func(w bool) { t.Logf("waiting for user: %v", w) })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("direct=%s ua=%s cookies=%d", res.DirectURL, res.UserAgent, len(res.Cookies))
	if !strings.HasPrefix(res.DirectURL, "http") {
		t.Errorf("bad direct url %q", res.DirectURL)
	}
}
