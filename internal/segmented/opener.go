package segmented

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Plain-HTTP transports, one per local source address. HTTP/2 is off so that
// every range request gets its own TCP connection, as with Python's urllib;
// compression is off so byte ranges are byte ranges.
var (
	transportsMu sync.Mutex
	transports   = map[string]*http.Transport{}
)

func transportFor(source string) *http.Transport {
	transportsMu.Lock()
	defer transportsMu.Unlock()
	if t, ok := transports[source]; ok {
		return t
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if source != "" {
		if ip := net.ParseIP(source); ip != nil {
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
		}
	}
	t := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 30 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
	transports[source] = t
	return t
}

// idleBody cancels the request when no bytes arrive for timeout, which is
// how urllib's socket timeout behaves (per read, not for the whole transfer).
type idleBody struct {
	rc     io.ReadCloser
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

// Do sends req with an idle timeout and wraps the result as a Response.
// Statuses >= 400 become *HTTPStatusError.
func Do(client *http.Client, req *http.Request, timeout time.Duration) (*Response, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(timeout, cancel)
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		timer.Stop()
		cancel()
		return nil, &HTTPStatusError{resp.StatusCode}
	}
	return &Response{
		Status: resp.StatusCode,
		Header: resp.Header.Get,
		Body:   &idleBody{rc: resp.Body, timer: timer, cancel: cancel, d: timeout},
	}, nil
}

// DefaultOpener is the plain transport. source is a local IP to send from
// (picks the network adapter that owns it); "" lets the OS choose.
func DefaultOpener(url string, headers map[string]string, timeout time.Duration, source string) (*Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: transportFor(source)}
	return Do(client, req, timeout)
}
