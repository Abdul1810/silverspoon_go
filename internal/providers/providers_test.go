package providers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestResolverHostsNeedResolution(t *testing.T) {
	for _, link := range []string{
		"https://fuckingfast.co/abc123",
		"https://www.fuckingfast.co/abc123#file.rar",
		"https://datanodes.to/xyz",
		"https://cdn.datanodes.to/d/xyz",
		"http://fuckingfast.co/x",
		"https://fuckingfast.co:443/abc123",
		"https://user@datanodes.to/xyz",
		"https://FuckingFast.co/abc123",
	} {
		if !NeedsResolution(link) {
			t.Errorf("NeedsResolution(%q) = false", link)
		}
	}
}

func TestGeneralHostsAreDirect(t *testing.T) {
	for _, link := range []string{
		"https://example.com/file.zip",
		"https://github.com/o/r/releases/download/v1/x.zip",
		"https://objects.githubusercontent.com/x/SilverSpoon.zip",
		"https://notfuckingfast.co.evil.com/x",
		"https://mydatanodes.to.example.com/x",
	} {
		if NeedsResolution(link) {
			t.Errorf("NeedsResolution(%q) = true", link)
		}
	}
}

func TestMalformedNeverCrashes(t *testing.T) {
	for _, bad := range []string{"", "not a url", "ftp://", "://///", "javascript:void"} {
		if NeedsResolution(bad) {
			t.Errorf("NeedsResolution(%q) = true", bad)
		}
	}
}

func TestFilenameFromLink(t *testing.T) {
	for _, c := range [][2]string{
		{"https://fuckingfast.co/abc123#Game_--_fitgirl-repacks.site_--_.part01.rar",
			"Game_--_fitgirl-repacks.site_--_.part01.rar"},
		{"https://github.com/o/r/releases/download/v1/SilverSpoon-windows.zip", "SilverSpoon-windows.zip"},
		{"https://cdn.example.com/files/My%20Game.zip?sig=abc&exp=1", "My Game.zip"},
		{"https://example.com/get?id=5", "get"},
		{"https://example.com/files/", "files"},
		{"https://example.com/", "example.com"},
		{"https://example.com/a%3Ab%2Fc.zip", "a_b_c.zip"},
	} {
		if got := FilenameFromLink(c[0]); got != c[1] {
			t.Errorf("FilenameFromLink(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestFilenameNeverEmptyOrInvalid(t *testing.T) {
	for _, bad := range []string{"", "https://", "https://x/%3F", "https://x/..."} {
		name := FilenameFromLink(bad)
		if name == "" || strings.ContainsAny(name, `<>:"/\|?*`) {
			t.Errorf("FilenameFromLink(%q) = %q", bad, name)
		}
	}
}

func TestFormHostsAreRoutedSeparately(t *testing.T) {
	link := "https://filekeeper.net/16moao3mygr6/Game_--_fitgirl-repacks.site_--_.part01.rar"
	if !NeedsFormResolution(link) || !NeedsFormResolution("https://www.filekeeper.net:443/x") {
		t.Error("filekeeper not routed to form resolution")
	}
	if NeedsResolution(link) {
		t.Error("filekeeper should not need the browser solver")
	}
	if NeedsFormResolution("https://notfilekeeper.net/x") {
		t.Error("notfilekeeper.net matched")
	}
	if got := FilenameFromLink(link); got != "Game_--_fitgirl-repacks.site_--_.part01.rar" {
		t.Errorf("got %q", got)
	}
}

func TestBzzhrGoesThroughSolverThenDirectDownload(t *testing.T) {
	link := "https://bzzhr.to/hjcobfz259jz"
	if !NeedsResolution(link) || !DownloadResolvedDirectly(link) || NeedsFormResolution(link) {
		t.Error("bzzhr routing wrong")
	}
	for _, other := range []string{"https://fuckingfast.co/abc#x.rar", "https://datanodes.to/abc/x.rar"} {
		if DownloadResolvedDirectly(other) {
			t.Errorf("DownloadResolvedDirectly(%q) = true", other)
		}
	}
	if got := FilenameFromLink(link); got != "hjcobfz259jz" {
		t.Errorf("got %q", got)
	}
}

func TestFilenameFromContentDisposition(t *testing.T) {
	for _, c := range [][2]string{
		{"attachment; filename=\"FIRE_AND_ASHES-SteamRIP.com.rar\"; " +
			"filename*=UTF-8''FIRE_AND_ASHES-SteamRIP.com.rar", "FIRE_AND_ASHES-SteamRIP.com.rar"},
		{`attachment; filename="My Game.part01.rar"`, "My Game.part01.rar"},
		{"attachment; filename*=UTF-8''%E2%82%AC%20rates.zip", "€ rates.zip"},
		{"attachment; filename=plain.zip", "plain.zip"},
		{`attachment; filename="../../evil:name.rar"`, "evil_name.rar"},
		{"inline", ""},
		{"", ""},
		{`attachment; filename=""`, ""},
	} {
		if got := FilenameFromContentDisposition(c[0]); got != c[1] {
			t.Errorf("FilenameFromContentDisposition(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestParseCountdown(t *testing.T) {
	page := "<div id=\"download-countdown\" class=\"w-full\"\n data-countdown=\"5\"\n" +
		" data-code=\"16moao3mygr6\" data-rand=\"\" data-method=\"Free &amp; fast\"\n" +
		" data-has-captcha=\"false\">"
	info := ParseCountdown(page)
	if info["countdown"] != "5" || info["code"] != "16moao3mygr6" {
		t.Errorf("info = %v", info)
	}
	if v, ok := info["rand"]; !ok || v != "" || info["method"] != "Free & fast" {
		t.Errorf("info = %v", info)
	}
	if len(ParseCountdown("<html>no countdown</html>")) != 0 {
		t.Error("expected empty")
	}
}

type post struct {
	form   map[string]string
	cookie string
}

// mockFormHost is a local stand-in for FileKeeper: /<code>/<name> sets a cookie
// and redirects to /download; POST op=download2 (with the cookie) redirects to
// the file.
func mockFormHost(captcha, password, code string, answer int) (*httptest.Server, string, func() []post) {
	var mu sync.Mutex
	var posts []post
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			data, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(data))
			form := map[string]string{}
			for k, v := range vals {
				form[k] = v[0]
			}
			mu.Lock()
			posts = append(posts, post{form, r.Header.Get("Cookie")})
			mu.Unlock()
			if answer == 302 {
				w.Header().Set("Location", "https://tunnel1.dlproxy.uk/download/XYZ?sig=1")
			}
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(answer)
		case strings.HasPrefix(r.URL.Path, "/download"):
			fmt.Fprintf(w, `<div id="download-countdown" data-countdown="2" data-code="%s" `+
				`data-referer="" data-rand="r1" data-method="" `+
				`data-has-password="%s" data-has-captcha="%s"></div>`, code, password, captcha)
		default:
			http.SetCookie(w, &http.Cookie{Name: "file_code", Value: code, Path: "/"})
			w.Header().Set("Location", "/download")
			w.WriteHeader(302)
		}
	}))
	return srv, srv.URL + "/" + code + "/Game.part01.rar", func() []post {
		mu.Lock()
		defer mu.Unlock()
		return posts
	}
}

func TestResolveFormLinkPostsTheDownloadForm(t *testing.T) {
	srv, link, posts := mockFormHost("false", "false", "abc123", 302)
	defer srv.Close()
	var waits []int
	got, err := ResolveFormLink(link, func(s int) { waits = append(waits, s) }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://tunnel1.dlproxy.uk/download/XYZ?sig=1" {
		t.Errorf("url = %q", got)
	}
	if !reflect.DeepEqual(waits, []int{3}) {
		t.Errorf("waits = %v", waits)
	}
	p := posts()[0]
	want := map[string]string{"op": "download2", "id": "abc123", "rand": "r1", "referer": "",
		"method_free": "Free download", "down_direct": "1"}
	if !reflect.DeepEqual(p.form, want) {
		t.Errorf("form = %v", p.form)
	}
	if !strings.Contains(p.cookie, "file_code=abc123") {
		t.Errorf("cookie = %q", p.cookie)
	}
}

func TestResolveFormLinkReportsBlockers(t *testing.T) {
	cases := []struct {
		captcha, password, code string
		answer                  int
		expected                string
	}{
		{"true", "false", "abc123", 302, "CAPTCHA"},
		{"false", "true", "abc123", 302, "password"},
		{"false", "false", "", 302, "no download button"},
		{"false", "false", "abc123", 200, "didn't return a download link"},
	}
	for _, c := range cases {
		srv, link, _ := mockFormHost(c.captcha, c.password, c.code, c.answer)
		_, err := ResolveFormLink(link, func(int) {}, 0)
		srv.Close()
		re, ok := err.(*ResolveError)
		if !ok {
			t.Errorf("%+v: no ResolveError, got %v", c, err)
			continue
		}
		if !strings.Contains(re.Error(), c.expected) {
			t.Errorf("%+v: %q", c, re.Error())
		}
	}
}
