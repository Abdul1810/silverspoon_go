package providers

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Folder pages list many files; ExpandFolder turns one into the file links
// so a whole upload can be queued without copying each link by hand.

var (
	// DataNodes folders: https://datanodes.to/f/<hash>
	dnFolderPath = regexp.MustCompile(`^/f/[A-Za-z0-9]+/?$`)
	// File links on a DataNodes page: https://datanodes.to/<12 chars>/<name>
	dnFileLink = regexp.MustCompile(`href="(https?://(?:www\.)?datanodes\.to/[a-z0-9]{12}/[^"]+)"`)
)

// IsFolderLink reports whether link is a folder page ExpandFolder can read.
func IsFolderLink(link string) bool {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || !hostMatches(link, []string{"datanodes.to"}) {
		return false
	}
	return dnFolderPath.MatchString(u.Path)
}

// maxFolderPages bounds the paging loop.
const maxFolderPages = 200

// ExpandFolder returns the file links listed on a folder page, following
// ?page=2, 3, ... until a page adds nothing new. Links are sorted naturally
// (part2 before part10).
func ExpandFolder(link string, client *http.Client) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for page := 1; page <= maxFolderPages; page++ {
		u := *base
		q := u.Query()
		q.Set("page", strconv.Itoa(page))
		u.RawQuery = q.Encode()
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", BrowserUA)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			return nil, &HTTPError{resp.StatusCode}
		}
		added := 0
		for _, m := range dnFileLink.FindAllStringSubmatch(string(body), -1) {
			l := strings.Replace(m[1], "http://", "https://", 1)
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
				added++
			}
		}
		if added == 0 {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no files found in the folder; it may be empty, private or removed")
	}
	sort.SliceStable(out, func(i, j int) bool { return naturalLess(FilenameFromLink(out[i]), FilenameFromLink(out[j])) })
	return out, nil
}

var digits = regexp.MustCompile(`\d+|\D+`)

// naturalLess compares strings with embedded numbers by value.
func naturalLess(a, b string) bool {
	pa, pb := digits.FindAllString(strings.ToLower(a), -1), digits.FindAllString(strings.ToLower(b), -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil {
			if na != nb {
				return na < nb
			}
			continue // "02" and "2" are the same number
		}
		return pa[i] < pb[i]
	}
	if len(pa) != len(pb) {
		return len(pa) < len(pb)
	}
	return a < b
}
