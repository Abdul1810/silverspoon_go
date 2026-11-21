// Package providers classifies download hosts and resolves form-based hosts.
//
// Port of providers.py. Hosts that hide the file behind a Cloudflare/Turnstile
// challenge need the solver to extract a direct link; form hosts only need a
// plain HTTP countdown + form post (see ResolveFormLink); every other host is
// treated as a plain, direct HTTP download.
package providers

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ResolverHosts are handled by the Turnstile/CAPTCHA solver. Keep in sync with
// the hosts the solver package actually supports.
var ResolverHosts = []string{"fuckingfast.co", "datanodes.to", "bzzhr.to"}

// DirectAfterSolveHosts are solver hosts whose resolved link works without the
// browser's cookies, so the file goes through the direct downloader.
var DirectAfterSolveHosts = []string{"bzzhr.to"}

// FormHosts have a countdown page followed by a form post (op=download2) that
// redirects to the file. No browser needed.
var FormHosts = []string{"filekeeper.net"}

// Characters Windows refuses in file names, plus ASCII control characters.
var invalidFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// BrowserUA is the User-Agent sent to form hosts.
const BrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// hostname mirrors urllib.parse.urlparse(link).hostname: no port, no userinfo,
// lower-cased; "" when there is none.
func hostname(link string) string {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func hostMatches(link string, hosts []string) bool {
	host := hostname(link)
	host = strings.TrimPrefix(host, "www.")
	if host == "" {
		return false
	}
	for _, h := range hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// NeedsResolution reports whether the link's host must go through the solver.
func NeedsResolution(link string) bool { return hostMatches(link, ResolverHosts) }

// DownloadResolvedDirectly reports whether the solver's resolved link can be
// fetched by the direct downloader.
func DownloadResolvedDirectly(link string) bool { return hostMatches(link, DirectAfterSolveHosts) }

// NeedsFormResolution reports whether the link's host needs ResolveFormLink.
func NeedsFormResolution(link string) bool { return hostMatches(link, FormHosts) }

// unquote is a lenient percent-decoder like urllib.parse.unquote: malformed
// escapes are kept literally and invalid UTF-8 becomes U+FFFD. With latin1 the
// decoded bytes are read as ISO-8859-1.
func unquote(s string, latin1 bool) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b = append(b, byte(v))
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	if latin1 {
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return string(r)
	}
	return strings.ToValidUTF8(string(b), "�")
}

func sanitize(raw string) string {
	return strings.Trim(invalidFilenameChars.ReplaceAllString(raw, "_"), " .")
}

var (
	cdExtended = regexp.MustCompile(`(?i)filename\*\s*=\s*([\w-]+)'[^']*'([^;]+)`)
	cdPlain    = regexp.MustCompile(`(?i)filename\s*=\s*"([^"]*)"|filename\s*=\s*([^;]+)`)
)

// FilenameFromContentDisposition returns the file name from a
// Content-Disposition header (RFC 6266/5987), or "" when there is none.
func FilenameFromContentDisposition(value string) string {
	if value == "" {
		return ""
	}
	var raw string
	if m := cdExtended.FindStringSubmatch(value); m != nil {
		enc := strings.ToLower(m[1])
		v := strings.Trim(strings.TrimSpace(m[2]), `"`)
		raw = unquote(v, enc == "iso-8859-1" || enc == "latin-1" || enc == "latin1")
	} else {
		idx := cdPlain.FindStringSubmatchIndex(value)
		if idx == nil {
			return ""
		}
		if idx[2] >= 0 {
			raw = value[idx[2]:idx[3]]
		} else {
			raw = value[idx[4]:idx[5]]
		}
		raw = strings.TrimSpace(raw)
	}
	parts := strings.Split(strings.ReplaceAll(raw, `\`, "/"), "/")
	return sanitize(parts[len(parts)-1])
}

// FilenameFromLink returns the local file name for a download link.
//
// FuckingFast-style links carry the name in the fragment (.../id#name.rar);
// otherwise it is the last path segment, ignoring any query string.
func FilenameFromLink(link string) string {
	link = strings.TrimSpace(link)
	var raw string
	if strings.Contains(link, "#") {
		parts := strings.Split(link, "#")
		raw = parts[len(parts)-1]
	} else if u, err := url.Parse(link); err == nil {
		p := strings.TrimRight(u.EscapedPath(), "/")
		segs := strings.Split(p, "/")
		raw = segs[len(segs)-1]
		if raw == "" {
			raw = strings.ToLower(u.Hostname())
		}
	} else {
		segs := strings.Split(strings.TrimRight(link, "/"), "/")
		raw = segs[len(segs)-1]
	}
	if name := sanitize(unquote(raw, false)); name != "" {
		return name
	}
	return "download"
}

// ResolveError means a form host page couldn't be turned into a direct link.
type ResolveError struct{ Msg string }

func (e *ResolveError) Error() string { return e.Msg }

var (
	countdownTag  = regexp.MustCompile(`(?i)<[a-z]+\s[^>]*\bid="download-countdown"[^>]*>`)
	countdownAttr = regexp.MustCompile(`(?i)\bdata-([\w-]+)="([^"]*)"`)
)

// ParseCountdown returns the data-* attributes of the page's
// #download-countdown element, keyed by lower-cased name.
func ParseCountdown(page string) map[string]string {
	out := map[string]string{}
	tag := countdownTag.FindString(page)
	if tag == "" {
		return out
	}
	for _, m := range countdownAttr.FindAllStringSubmatch(tag, -1) {
		out[strings.ToLower(m[1])] = html.UnescapeString(m[2])
	}
	return out
}

// HTTPError mirrors urllib's HTTPError message for a failed page request.
type HTTPError struct{ Code int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP Error %d: %s", e.Code, http.StatusText(e.Code))
}

// ResolveFormLink returns the direct download URL for a FileKeeper-style link:
// open the page, wait out its countdown, then post the form its "Free download"
// button posts (op=download2) and return the redirect target. sleep(seconds)
// does the waiting, so callers can make it stop early on cancel.
func ResolveFormLink(link string, sleep func(seconds int), timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if sleep == nil {
		sleep = func(s int) { time.Sleep(time.Duration(s) * time.Second) }
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: timeout}

	// The file link sets a file_code cookie and redirects to /download.
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", BrowserUA)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", &HTTPError{resp.StatusCode}
	}
	pageURL := resp.Request.URL.String()
	page := strings.ToValidUTF8(string(body), "�")

	info := ParseCountdown(page)
	if info["code"] == "" {
		return "", &ResolveError{"The file page has no download button. The file may have been removed, " +
			"or the page layout changed."}
	}
	if info["has-captcha"] == "true" {
		return "", &ResolveError{"The host is asking for a CAPTCHA for this file, which SilverSpoon can't fill in."}
	}
	if info["has-password"] == "true" {
		return "", &ResolveError{"This file is password-protected."}
	}

	countdown, err := strconv.Atoi(strings.TrimSpace(info["countdown"]))
	if err != nil {
		countdown = 0
	}
	if countdown > 0 {
		sleep(countdown + 1) // the server checks the wait; one spare second
	}

	method := info["method"]
	if method == "" {
		method = "Free download"
	}
	pairs := [][2]string{
		{"op", "download2"},
		{"id", info["code"]},
		{"rand", info["rand"]},
		{"referer", info["referer"]},
		{"method_free", method},
		{"down_direct", "1"},
	}
	enc := make([]string, len(pairs))
	for i, p := range pairs {
		enc[i] = url.QueryEscape(p[0]) + "=" + url.QueryEscape(p[1])
	}
	form := strings.Join(enc, "&")

	poster := &http.Client{
		Jar:     jar,
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	preq, err := http.NewRequest("POST", pageURL, strings.NewReader(form))
	if err != nil {
		return "", err
	}
	preq.Header.Set("User-Agent", BrowserUA)
	preq.Header.Set("Referer", pageURL)
	preq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	presp, err := poster.Do(preq)
	if err != nil {
		return "", err
	}
	io.Copy(io.Discard, presp.Body)
	presp.Body.Close()

	location := ""
	switch presp.StatusCode {
	case 301, 302, 303, 307, 308:
		location = presp.Header.Get("Location")
	}
	if location == "" {
		return "", &ResolveError{fmt.Sprintf("The host didn't return a download link (HTTP %d). "+
			"The file may be unavailable or the download limit reached.", presp.StatusCode)}
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return location, nil
	}
	ref, err := url.Parse(location)
	if err != nil {
		return location, nil
	}
	return base.ResolveReference(ref).String(), nil
}
