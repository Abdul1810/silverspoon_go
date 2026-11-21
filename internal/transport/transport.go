// Package transport provides the browser-fingerprinted HTTP client used for
// solver hosts (the Go stand-in for curl_cffi's impersonate="chrome") and
// local-address helpers for binding connections to a network adapter.
package transport

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/imroc/req/v3"

	"silverspoon/internal/segmented"
)

// LocalIPv4Addresses returns usable local IPv4 addresses (one per connected
// adapter, typically), skipping loopback and link-local.
func LocalIPv4Addresses() []string {
	var found []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil {
				continue
			}
			s := ip.String()
			if strings.HasPrefix(s, "127.") || strings.HasPrefix(s, "169.254.") {
				continue
			}
			dup := false
			for _, f := range found {
				if f == s {
					dup = true
				}
			}
			if !dup {
				found = append(found, s)
			}
		}
	}
	return found
}

// ParseSources splits the comma-separated bind_addresses setting.
func ParseSources(setting string) []string {
	var out []string
	for _, p := range strings.Split(setting, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Dialer returns a dial function that leaves from source ("" = OS choice).
func Dialer(source string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if ip := net.ParseIP(source); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return d.DialContext
}

// ChromeClient is a fresh client with Chrome's TLS/HTTP2 fingerprint, sending
// from source. Like the Python app's per-request curl session, each call gives
// a new client, so each request gets its own connection.
func ChromeClient(source string) *req.Client {
	c := req.C().ImpersonateChrome().SetDial(Dialer(source)).SetTLSHandshakeTimeout(30 * time.Second)
	c.SetRedirectPolicy(req.MaxRedirectPolicy(30))
	return c
}

func cookieList(cookies map[string]string) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(cookies))
	for k, v := range cookies {
		out = append(out, &http.Cookie{Name: k, Value: v})
	}
	return out
}

// ChromeOpener is a segmented.Opener that keeps the browser-like fingerprint
// and the solver's cookies.
func ChromeOpener(cookies map[string]string) segmented.Opener {
	return func(url string, headers map[string]string, timeout time.Duration, source string) (*segmented.Response, error) {
		return ChromeGet(context.Background(), url, headers, cookies, timeout, source)
	}
}

// ChromeGet streams a GET with the Chrome fingerprint. Statuses >= 400 become
// *segmented.HTTPStatusError. timeout is an idle timeout, not a total one.
func ChromeGet(ctx context.Context, url string, headers, cookies map[string]string, timeout time.Duration, source string) (*segmented.Response, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c := ChromeClient(source)
	ctx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(timeout, cancel)
	resp, err := c.R().SetContext(ctx).SetHeaders(headers).SetCookies(cookieList(cookies)...).
		DisableAutoReadResponse().Get(url)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		timer.Stop()
		cancel()
		return nil, &segmented.HTTPStatusError{Code: resp.StatusCode}
	}
	return &segmented.Response{
		Status: resp.StatusCode,
		Header: resp.Header.Get,
		Body:   &idleBody{rc: resp.Body, timer: timer, cancel: cancel, d: timeout},
	}, nil
}

// ChromeHead sends a HEAD with the Chrome fingerprint and returns status and
// headers.
func ChromeHead(ctx context.Context, url string, headers, cookies map[string]string, timeout time.Duration, source string) (int, http.Header, error) {
	c := ChromeClient(source).SetTimeout(timeout)
	resp, err := c.R().SetContext(ctx).SetHeaders(headers).SetCookies(cookieList(cookies)...).Head(url)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, resp.Header, nil
}

type idleBody struct {
	rc interface {
		Read([]byte) (int, error)
		Close() error
	}
	timer  *time.Timer
	cancel context.CancelFunc
	d      time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.d)
	return b.rc.Read(p)
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.rc.Close()
	b.cancel()
	return err
}
