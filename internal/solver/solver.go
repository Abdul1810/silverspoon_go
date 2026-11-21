// Package solver drives a real Chromium over CDP to get past Cloudflare
// Turnstile and resolve direct download links for FuckingFast, DataNodes and
// BZZHR (port of cf_turnstile.py).
package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Log receives the solver's progress lines; the app points it at its log file.
var Log = slog.Default()

const (
	// MaxAttempts is how many token waits a FuckingFast page gets.
	MaxAttempts = 2
	// UserSolveTime is the extra time the user gets once the window is shown
	// for a check that didn't pass on its own.
	UserSolveTime = 90
)

// Result is a resolved link plus what the downloader needs to fetch it.
type Result struct {
	DirectURL string
	Cookies   map[string]string
	UserAgent string
}

// Solver owns one browser; resolutions are serialised.
type Solver struct {
	TokenTimeout int  // seconds
	ShowWindow   bool // takes effect the next time the browser starts

	profile  string
	mu       sync.Mutex // the browser lock
	b        *browserProc
	tab      *tab
	notify   func(bool)
	revealed bool
	stopReq  atomic.Bool

	stateMu   sync.Mutex
	userAgent string
	cookies   map[string]string
}

// New returns a solver. profile "" uses %TEMP%/silverspoon_browser_profile.
func New(profile string, timeout int, showWindow bool) *Solver {
	if profile == "" {
		profile = filepath.Join(os.TempDir(), "silverspoon_browser_profile")
	}
	return &Solver{TokenTimeout: timeout, ShowWindow: showWindow, profile: profile}
}

// UserAgent is the browser's UA from the last resolution.
func (s *Solver) UserAgent() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.userAgent
}

// Cookies are the browser's cookies from the last resolution.
func (s *Solver) Cookies() map[string]string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.cookies == nil {
		return map[string]string{}
	}
	return s.cookies
}

type tab struct {
	b         *browserProc
	targetID  string
	sessionID string
}

func (t *tab) call(ctx context.Context, method string, params, out any) error {
	return t.b.conn.call(ctx, t.sessionID, method, params, out)
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// evaluate runs expr and returns its value as a string (expressions are
// wrapped in JSON.stringify, so results come back as JSON text).
func (t *tab) evaluate(expr string, awaitPromise bool, timeout time.Duration) (string, error) {
	ctx, cancel := withTimeout(timeout)
	defer cancel()
	var res struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := t.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": awaitPromise,
	}, &res)
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil {
			msg = res.ExceptionDetails.Exception.Description
		}
		return "", errors.New(msg)
	}
	var s string
	if json.Unmarshal(res.Result.Value, &s) != nil {
		return "", fmt.Errorf("unexpected evaluation result type %q", res.Result.Type)
	}
	return s, nil
}

func (t *tab) setWindow(left, top, width, height int) error {
	ctx, cancel := withTimeout(5 * time.Second)
	defer cancel()
	var w struct {
		WindowID int `json:"windowId"`
	}
	if err := t.b.conn.call(ctx, "", "Browser.getWindowForTarget", map[string]any{"targetId": t.targetID}, &w); err != nil {
		return err
	}
	return t.b.conn.call(ctx, "", "Browser.setWindowBounds", map[string]any{
		"windowId": w.WindowID,
		"bounds":   map[string]any{"left": left, "top": top, "width": width, "height": height, "windowState": "normal"},
	}, nil)
}

func (t *tab) bringToFront() error {
	ctx, cancel := withTimeout(5 * time.Second)
	defer cancel()
	return t.call(ctx, "Page.bringToFront", nil, nil)
}

func (t *tab) navigate(link, referrer string) error {
	ctx, cancel := withTimeout(15 * time.Second)
	defer cancel()
	p := map[string]any{"url": link}
	if referrer != "" {
		p["referrer"] = referrer
	}
	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := t.call(ctx, "Page.navigate", p, &res); err != nil {
		return err
	}
	if res.ErrorText != "" && res.ErrorText != "net::ERR_ABORTED" {
		return errors.New(res.ErrorText)
	}
	return nil
}

const popupBlockerJS = `(() => {
    Object.defineProperty(window, 'open', {
        configurable: true, writable: false, value: () => null,
    });
    const blockBlank = e => {
        const el = e.target?.closest?.('a[target="_blank"], form[target="_blank"]');
        if (el) { el.target = '_self'; e.preventDefault(); }
    };
    document.addEventListener('click', blockBlank, true);
    document.addEventListener('auxclick', blockBlank, true);
    const origAnchorClick = HTMLAnchorElement.prototype.click;
    HTMLAnchorElement.prototype.click = function () {
        if (this.target === '_blank') this.target = '_self';
        return origAnchorClick.call(this);
    };
    const origFormSubmit = HTMLFormElement.prototype.submit;
    HTMLFormElement.prototype.submit = function () {
        if (this.target === '_blank') this.target = '_self';
        return origFormSubmit.call(this);
    };
    new MutationObserver(records => {
        for (const r of records)
            for (const n of r.addedNodes)
                if (n.nodeType === 1 && n.getAttribute?.('target') === '_blank')
                    n.setAttribute('target', '_self');
    }).observe(document, {childList: true, subtree: true});
})();`

// openTab attaches to the browser's page (about:blank), creating one if
// needed, and installs the popup blocker.
func (s *Solver) openTab() (*tab, error) {
	ctx, cancel := withTimeout(15 * time.Second)
	defer cancel()
	b := s.b
	var targetID string
	for i := 0; i < 50 && targetID == ""; i++ {
		ts, err := b.targets(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if t.Type == "page" {
				targetID = t.TargetID
				break
			}
		}
		if targetID == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if targetID == "" {
		var res struct {
			TargetID string `json:"targetId"`
		}
		if err := b.conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &res); err != nil {
			return nil, err
		}
		targetID = res.TargetID
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, &att); err != nil {
		return nil, err
	}
	t := &tab{b: b, targetID: targetID, sessionID: att.SessionID}
	// Page.enable makes the popup blocker below apply to new documents.
	if err := t.call(ctx, "Page.enable", nil, nil); err != nil {
		return nil, err
	}
	if err := t.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": popupBlockerJS}, nil); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Solver) closeBrowser() {
	if s.b != nil {
		Log.Info("TurnstileSolver: stopping browser")
		s.b.stop()
		s.b = nil
		s.tab = nil
	}
}

func (s *Solver) ensureBrowser() error {
	if s.b != nil {
		return nil
	}
	candidates := FindBrowsers()
	if len(candidates) == 0 {
		return errors.New("Bundled Playwright Chromium was not found. Reinstall " +
			"SilverSpoon and keep the playwright-chromium folder beside the executable.")
	}
	var lastErr error
	for _, bp := range candidates {
		Log.Info(fmt.Sprintf("TurnstileSolver: starting browser %s", bp))
		b, err := launchBrowser(bp, s.profile, s.ShowWindow)
		if err != nil {
			lastErr = err
			Log.Warn(fmt.Sprintf("TurnstileSolver: %s failed: %v", bp, err))
			continue
		}
		s.b = b
		t, err := s.openTab()
		if err != nil {
			lastErr = err
			Log.Warn(fmt.Sprintf("TurnstileSolver: %s failed: %v", bp, err))
			b.stop()
			s.b = nil
			continue
		}
		s.tab = t
		Log.Info("TurnstileSolver: browser ready, popup creation blocked")
		return nil
	}
	return fmt.Errorf("No candidate browser could start. Last error: %v", lastErr)
}

func sleepSec(n float64) { time.Sleep(time.Duration(n * float64(time.Second))) }

func (s *Solver) navigateTo(link, referrer string) (*tab, error) {
	if s.tab == nil {
		Log.Info("TurnstileSolver: retained tab missing; recreating")
		t, err := s.openTab()
		if err != nil {
			return nil, errors.New("Failed to create new tab in browser.")
		}
		s.tab = t
	}
	t := s.tab
	ctx, cancel := withTimeout(5 * time.Second)
	before, _ := s.b.targets(ctx)
	cancel()
	Log.Info(fmt.Sprintf("TurnstileSolver: navigating tab to %s; targets_before=%d", link, len(before)))
	if err := t.navigate(link, referrer); err != nil {
		Log.Warn(fmt.Sprintf("TurnstileSolver: navigation timed out or failed (%v). Tab is likely dead.", err))
		s.tab = nil
		return nil, errors.New("Tab navigation failed/hung.")
	}
	sleepSec(2)
	ctx, cancel = withTimeout(5 * time.Second)
	after, _ := s.b.targets(ctx)
	cancel()
	Log.Info(fmt.Sprintf("TurnstileSolver: navigation settled; targets=%d (delta=%+d)", len(after), len(after)-len(before)))

	pageURL := ""
	if raw, err := t.evaluate("JSON.stringify(location.href)", false, 5*time.Second); err == nil {
		json.Unmarshal([]byte(raw), &pageURL)
	}
	// bzzhr.to may send the browser to the uploader's site (e.g. steamrip.com);
	// resolveBzzhr handles that, so it isn't a hijack there.
	if !(strings.HasPrefix(pageURL, "https://fuckingfast.co/") || strings.Contains(pageURL, "datanodes.to") ||
		strings.Contains(pageURL, "bzzhr.to") || (strings.Contains(link, "bzzhr.to") && strings.HasPrefix(pageURL, "http"))) {
		Log.Warn(fmt.Sprintf("TurnstileSolver: unexpected URL after navigation: %s; tab might be hijacked or blank.", pageURL))
		s.tab = nil
		return nil, errors.New("Tab hijacked or failed to load correct URL.")
	}
	Log.Info(fmt.Sprintf("TurnstileSolver: page loaded: %s", pageURL))
	return t, nil
}

// askUser brings the (normally off-screen) window in front of the user so
// they can complete a check that didn't pass on its own.
func (s *Solver) askUser(t *tab) {
	if s.revealed {
		return
	}
	s.revealed = true
	Log.Warn("TurnstileSolver: check didn't pass on its own; showing the window for the user")
	var err error
	if !s.ShowWindow {
		err = t.setWindow(50, 50, 500, 550)
	}
	if err == nil {
		err = t.bringToFront()
	}
	if err != nil {
		Log.Warn(fmt.Sprintf("TurnstileSolver: could not show the window: %v", err))
	}
	if s.notify != nil {
		s.notify(true)
	}
}

// userDone sends the window back off-screen once the check has passed.
func (s *Solver) userDone(t *tab) {
	if !s.revealed {
		return
	}
	s.revealed = false
	if !s.ShowWindow {
		if err := t.setWindow(-32000, -32000, 500, 550); err != nil {
			Log.Warn(fmt.Sprintf("TurnstileSolver: could not hide the window: %v", err))
		}
	}
	if s.notify != nil {
		s.notify(false)
	}
}

func (s *Solver) resolveFuckingFast(link string, t *tab) (string, error) {
	parts := strings.Split(link, "/")
	fileID := strings.Split(parts[len(parts)-1], "#")[0]
	var state struct {
		Token     string `json:"token"`
		HasWidget bool   `json:"hasWidget"`
	}
	if raw, err := t.evaluate("JSON.stringify({token:window.turnstileToken||"+
		"document.querySelector('[name=\"cf-turnstile-response\"]')?.value||'',"+
		"hasWidget:!!(document.querySelector('.cf-turnstile')||"+
		"document.querySelector('[name=\"cf-turnstile-response\"]')||"+
		"document.querySelector('iframe[src*=\"turnstile\"]'))})", false, 30*time.Second); err == nil {
		json.Unmarshal([]byte(raw), &state)
	}
	token := state.Token
	Log.Info(fmt.Sprintf("TurnstileSolver (FuckingFast): has_widget=%s token_present=%s",
		pyBool(state.HasWidget), pyBool(len(token) > 20)))

	if state.HasWidget && len(token) <= 20 {
		for attempt := 0; attempt < MaxAttempts; attempt++ {
			last := attempt == MaxAttempts-1
			// The last attempt runs with the window shown, so it also gets
			// time for the user to click an interactive check.
			wait := s.TokenTimeout
			if last {
				wait += UserSolveTime
			}
			for i := 0; i < wait; i++ {
				if s.stopReq.Load() {
					return "", errors.New("Solver stopped while waiting for token.")
				}
				raw, err := t.evaluate("JSON.stringify(window.turnstileToken || "+
					"document.querySelector('[name=\"cf-turnstile-response\"]')?.value || null)", false, 2*time.Second)
				if err == nil {
					var tok *string
					if json.Unmarshal([]byte(raw), &tok) == nil && tok != nil {
						token = *tok
					} else {
						token = ""
					}
					if len(token) > 20 {
						Log.Info(fmt.Sprintf("TurnstileSolver (FuckingFast): token received after %ds", i+1))
						break
					}
				}
				if i%5 == 0 {
					Log.Info(fmt.Sprintf("TurnstileSolver (FuckingFast): waiting for token; attempt=%d elapsed=%ds", attempt+1, i))
				}
				sleepSec(1)
			}
			if len(token) > 20 {
				break
			}
			if !last {
				Log.Warn("TurnstileSolver (FuckingFast): no token yet; reloading with the window shown")
				s.tab = nil
				nt, err := s.navigateTo(link, "")
				if err != nil {
					return "", err
				}
				t = nt
				s.askUser(t)
				sleepSec(3)
			}
		}
		if len(token) <= 20 {
			waited := s.TokenTimeout*MaxAttempts + UserSolveTime
			return "", fmt.Errorf("Cloudflare Turnstile was not completed after %d seconds. "+
				"Your IP may be flagged, or the check in the browser window wasn't completed.", waited)
		}
		s.userDone(t)
	}

	fetchJS := "(async()=>{const r=await fetch('/f/" + fileID + "/go'," +
		"{method:'POST',headers:{'HX-Request':'true','HX-Target':''," +
		"'HX-Current-URL':'" + link + "','Referer':'" + link + "'}," +
		"body:new URLSearchParams({'cf-turnstile-response':window.turnstileToken||" +
		"document.querySelector('[name=\"cf-turnstile-response\"]')?.value||''})});" +
		"const h=r.headers.get('Hx-Redirect')||r.headers.get('hx-redirect');" +
		"const t=await r.text();" +
		"return JSON.stringify({status:r.status,hxRedirect:h," +
		"bodyLen:t.length,bodyHead:t.substring(0,200)});})()"
	raw, err := t.evaluate(fetchJS, true, 60*time.Second)
	if err != nil {
		return "", err
	}
	var result struct {
		Status     int     `json:"status"`
		HxRedirect *string `json:"hxRedirect"`
		BodyHead   string  `json:"bodyHead"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return "", fmt.Errorf("Unexpected fetch result: %v", err)
	}
	direct := ""
	if result.HxRedirect != nil {
		direct = *result.HxRedirect
	}
	Log.Info(fmt.Sprintf("TurnstileSolver (FuckingFast): POST /go status=%d redirect=%s body=%s",
		result.Status, pyBool(direct != ""), trunc(result.BodyHead, 200)))
	if result.Status != 200 || direct == "" {
		return "", fmt.Errorf("Could not resolve direct link. POST /go returned HTTP %d. Response: %s",
			result.Status, result.BodyHead)
	}
	return direct, nil
}

// DataNodes:
//
//	step 1  "Continue to Download" (#method_free, enabled after a short
//	        "Preparing File" animation) -> form POST op=download1
//	step 2  Turnstile widget; the button only renders once it passes:
//	        "Free Download" -> countdown ("Ready in Ns") -> "Start Download"
//	        -> fetch POST op=download2 answering JSON {url}
//
// Each page load wipes window state, so the fetch hook is (re)installed on
// every tick. The hook keeps the URL and hands the page an empty reply, so the
// solver's own browser never starts downloading the file itself.
const datanodesStepJS = `
        JSON.stringify((() => {
            if (!window.__dn_hooked) {
                window.__dn_hooked = true;
                window.__dn_direct_url = null;
                const origFetch = window.fetch;
                window.fetch = async function(...args) {
                    const resp = await origFetch.apply(this, args);
                    try {
                        const data = await resp.clone().json();
                        if (data && data.url) {
                            window.__dn_direct_url = decodeURIComponent(data.url);
                            return new Response('{}', {status: 200, headers: {'Content-Type': 'application/json'}});
                        }
                    } catch (e) {}
                    return resp;
                };
            }
            if (window.__dn_direct_url) {
                return {url: window.__dn_direct_url};
            }
            const links = Array.from(document.querySelectorAll('a')).map(a => a.href).filter(h => h && (h.includes('dlproxy') || h.includes('.rar') || (h.includes(':8080') && !h.includes('datanodes.to/app'))));
            if (links.length) {
                return {url: links[0]};
            }
            const hasCaptcha = !!document.querySelector('.cf-turnstile, iframe[src*="turnstile"]');
            const btns = Array.from(document.querySelectorAll('button, input[type="submit"]'));
            for (const b of btns) {
                // First line only: the buttons carry a subtitle ("Standard speed").
                const txt = (b.innerText || b.value || '').trim().split('\n')[0].trim();
                if (!/^(Continue to Download|Free Download|Start Download)/i.test(txt)) continue;
                // Disabled while "Preparing File" runs; give a click 5 s to take effect.
                if (b.disabled || !b.offsetParent || Date.now() - (+b.dataset.dnClicked || 0) < 5000) {
                    return {waiting: txt, hasCaptcha};
                }
                b.dataset.dnClicked = Date.now();
                b.scrollIntoView({behavior: 'instant', block: 'center'});
                b.click();
                return {clicked: txt, hasCaptcha};
            }
            return {hasCaptcha};
        })())
`

func (s *Solver) resolveDataNodes(link string, t *tab) (string, error) {
	var mu sync.Mutex
	directURL := ""
	unsub := t.b.conn.subscribe(func(method, session string, params json.RawMessage) {
		if method != "Network.responseReceived" || session != t.sessionID {
			return
		}
		var ev struct {
			Response struct {
				URL string `json:"url"`
			} `json:"response"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		u := ev.Response.URL
		if (strings.Contains(u, "dlproxy") || strings.Contains(u, "/d/") || strings.Contains(u, ".rar")) &&
			!strings.Contains(u, "datanodes.to") {
			mu.Lock()
			directURL = u
			mu.Unlock()
			Log.Info(fmt.Sprintf("TurnstileSolver (DataNodes): captured direct URL via CDP network: %s", trunc(u, 60)))
		}
	})
	defer unsub()
	ctx, cancel := withTimeout(5 * time.Second)
	t.call(ctx, "Network.enable", nil, nil)
	cancel()
	got := func() string {
		mu.Lock()
		defer mu.Unlock()
		return directURL
	}

	Log.Info("TurnstileSolver (DataNodes): working through download steps...")
	// Turnstile gets revealAfter seconds to pass on its own, then the window is
	// shown and the user gets UserSolveTime more to complete it.
	revealAfter := max(10, s.TokenTimeout)
	captchaTimeout := revealAfter + UserSolveTime
	deadline := time.Now().Add(time.Duration(max(30, s.TokenTimeout+15)+45) * time.Second)
	var captchaSince time.Time
	var state struct {
		URL        string `json:"url"`
		Clicked    string `json:"clicked"`
		Waiting    string `json:"waiting"`
		HasCaptcha bool   `json:"hasCaptcha"`
	}
	for got() == "" {
		if s.stopReq.Load() {
			return "", errors.New("Solver stopped while waiting for DataNodes download link.")
		}
		now := time.Now()
		if now.After(deadline) {
			break
		}
		sleepSec(1)
		raw, err := t.evaluate(datanodesStepJS, false, 10*time.Second)
		if err != nil {
			continue // mid-navigation (step 1 submits a form); try next tick
		}
		state.URL, state.Clicked, state.Waiting, state.HasCaptcha = "", "", "", false
		if json.Unmarshal([]byte(raw), &state) != nil {
			continue
		}
		if state.URL != "" {
			mu.Lock()
			directURL = state.URL
			mu.Unlock()
			Log.Info(fmt.Sprintf("TurnstileSolver (DataNodes): captured direct URL from page state: %s", trunc(state.URL, 60)))
			break
		}
		if state.Clicked != "" {
			Log.Info(fmt.Sprintf("TurnstileSolver (DataNodes): clicked '%s'", state.Clicked))
		}
		// Turnstile showing with no download button yet means it hasn't passed.
		if state.HasCaptcha && state.Clicked == "" && state.Waiting == "" {
			if captchaSince.IsZero() {
				captchaSince = now
			}
			el := now.Sub(captchaSince).Seconds()
			if el > float64(revealAfter) && !s.revealed {
				s.askUser(t)
				deadline = deadline.Add(UserSolveTime * time.Second)
			}
			if el > float64(captchaTimeout) {
				return "", fmt.Errorf("DataNodes Turnstile was not completed after %d seconds. "+
					"Your IP may be flagged, or the check in the browser window wasn't completed.", captchaTimeout)
			}
		} else {
			captchaSince = time.Time{}
			s.userDone(t) // passed (no-op if it was never shown)
		}
	}
	if d := got(); d != "" {
		return d, nil
	}
	lastStep := state.Clicked
	if lastStep == "" {
		lastStep = state.Waiting
	}
	if lastStep == "" {
		lastStep = "no download button"
	}
	return "", fmt.Errorf("DataNodes did not produce a direct download link "+
		"(last step seen: %s). The page layout may have changed.", lastStep)
}

// bzzhr.to: the whole site sits behind a Cloudflare "Just a moment..." page;
// past it, the file page's "Download File" button is an htmx GET
// (/<code>/download?t=<signed>) answering 204 with an HX-Redirect to the file.
const bzzhrStateJS = `
        JSON.stringify((() => {
            const btn = document.querySelector('a.download-btn[hx-get]');
            return {
                origin: location.origin,
                href: btn ? btn.getAttribute('hx-get') : null,
                challenge: !btn && (/just a moment/i.test(document.title)
                    || !!document.querySelector('iframe[src*="challenges.cloudflare.com"], [name="cf-turnstile-response"]')),
            };
        })())
`

func (s *Solver) resolveBzzhr(link string, t *tab) (string, error) {
	revealAfter := max(10, s.TokenTimeout)
	challengeTimeout := revealAfter + UserSolveTime
	start := time.Now()
	var challengeSince time.Time
	href := ""
	partner := ""
	linkOrigin := ""
	if u, err := url.Parse(link); err == nil {
		linkOrigin = u.Scheme + "://" + u.Host
	}
	for href == "" {
		if s.stopReq.Load() {
			return "", errors.New("Solver stopped while waiting for the bzzhr.to page.")
		}
		sleepSec(1)
		now := time.Now()
		raw, err := t.evaluate(bzzhrStateJS, false, 10*time.Second)
		if err != nil {
			continue // mid-navigation (the challenge reloads the page)
		}
		var state struct {
			Origin    string  `json:"origin"`
			Href      *string `json:"href"`
			Challenge bool    `json:"challenge"`
		}
		if json.Unmarshal([]byte(raw), &state) != nil {
			continue
		}
		if state.Href != nil && *state.Href != "" {
			href = *state.Href
			break
		}
		if state.Origin != "" && state.Origin != linkOrigin {
			// bzzhr only serves a partner's upload to visitors coming from the
			// partner's site; anyone else is sent there. Come back the way a
			// click on that site would, with it as the referrer.
			if partner != "" {
				return "", fmt.Errorf("bzzhr.to keeps sending the browser to %s instead of the file page.", state.Origin)
			}
			partner = state.Origin + "/"
			Log.Info(fmt.Sprintf("TurnstileSolver (bzzhr): sent to %s; reopening the link with it as referrer", partner))
			nt, err := s.navigateTo(link, partner)
			if err != nil {
				return "", err
			}
			t = nt
			start, challengeSince = time.Now(), time.Time{}
			continue
		}
		if state.Challenge {
			if challengeSince.IsZero() {
				challengeSince = now
			}
			el := now.Sub(challengeSince).Seconds()
			if el > float64(revealAfter) && !s.revealed {
				s.askUser(t)
			}
			if el > float64(challengeTimeout) {
				return "", fmt.Errorf("bzzhr.to's Cloudflare check was not completed after %d seconds. "+
					"Your IP may be flagged, or the check in the browser window wasn't completed.", challengeTimeout)
			}
		} else if now.Sub(start) > 30*time.Second {
			return "", errors.New("The bzzhr.to page has no download button. The file may have been removed, " +
				"or the page layout changed.")
		}
	}
	s.userDone(t)
	Log.Info("TurnstileSolver (bzzhr): page ready; requesting download link")

	hrefJSON, _ := json.Marshal(href)
	raw, err := t.evaluate("(async () => { const r = await fetch("+string(hrefJSON)+", {headers: "+
		"{'HX-Request': 'true', 'HX-Current-URL': location.href}, redirect: 'manual'});"+
		" return JSON.stringify({status: r.status, hx: r.headers.get('HX-Redirect')}); })()", true, 60*time.Second)
	if err != nil {
		return "", err
	}
	var result struct {
		Status int     `json:"status"`
		Hx     *string `json:"hx"`
	}
	json.Unmarshal([]byte(raw), &result)
	if result.Hx == nil || *result.Hx == "" {
		return "", fmt.Errorf("bzzhr.to didn't return a download link (HTTP %d). "+
			"The link may have expired or the file is unavailable.", result.Status)
	}
	base, err1 := url.Parse(link)
	ref, err2 := url.Parse(*result.Hx)
	if err1 != nil || err2 != nil {
		return *result.Hx, nil
	}
	return base.ResolveReference(ref).String(), nil
}

func (s *Solver) resolve(link string, notify func(bool)) (*Result, error) {
	s.notify, s.revealed = notify, false
	if err := s.ensureBrowser(); err != nil {
		return nil, err
	}
	Log.Info(fmt.Sprintf("TurnstileSolver: resolving %s", link))
	t, err := s.navigateTo(link, "")
	if err != nil {
		Log.Error(fmt.Sprintf("TurnstileSolver: initial navigation failed: %v", err))
		s.closeBrowser()
		if err := s.ensureBrowser(); err != nil {
			return nil, err
		}
		if t, err = s.navigateTo(link, ""); err != nil {
			return nil, err
		}
	}
	sleepSec(2)

	var direct string
	func() {
		defer func() {
			// Never leave the window on screen (a failure closes the browser).
			if s.revealed && s.tab != nil {
				s.userDone(s.tab)
			}
			s.notify, s.revealed = nil, false
		}()
		low := strings.ToLower(link)
		switch {
		case strings.Contains(low, "datanodes.to"):
			direct, err = s.resolveDataNodes(link, t)
		case strings.Contains(low, "bzzhr.to"):
			direct, err = s.resolveBzzhr(link, t)
		default:
			direct, err = s.resolveFuckingFast(link, t)
		}
	}()
	if err != nil {
		return nil, err
	}

	ua := ""
	if raw, err := t.evaluate("JSON.stringify(navigator.userAgent)", false, 10*time.Second); err == nil {
		json.Unmarshal([]byte(raw), &ua)
	}
	cookies := map[string]string{}
	ctx, cancel := withTimeout(10 * time.Second)
	var cres struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	}
	if err := s.b.conn.call(ctx, "", "Storage.getCookies", nil, &cres); err == nil {
		for _, c := range cres.Cookies {
			cookies[c.Name] = c.Value
		}
	}
	cancel()
	s.stateMu.Lock()
	s.userAgent, s.cookies = ua, cookies
	s.stateMu.Unlock()
	Log.Info(fmt.Sprintf("TurnstileSolver: resolved -> %s", trunc(direct, 60)))

	// Navigating back to about:blank cleans up the solver window visually and
	// stops the page executing scripts or using RAM until the next link.
	if s.tab != nil {
		if err := s.tab.navigate("about:blank", ""); err != nil {
			Log.Warn(fmt.Sprintf("TurnstileSolver: could not clear tab after solve: %v", err))
		}
	}
	return &Result{DirectURL: direct, Cookies: cookies, UserAgent: ua}, nil
}

// GetDirectLink resolves link. notify(true) is called when the window is
// shown because the user must complete the check, notify(false) once it's
// hidden again. Any failure tears the browser down so a broken instance is
// never reused.
func (s *Solver) GetDirectLink(link string, notify func(bool)) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopReq.Store(false)
	res, err := s.resolve(link, notify)
	if err != nil {
		s.closeBrowser()
		return nil, err
	}
	return res, nil
}

// Stop interrupts any wait and closes the browser.
func (s *Solver) Stop() {
	s.stopReq.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeBrowser()
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
