// Package update checks GitHub for new releases and applies a downloaded one
// (port of update_logic.py and MainWindow._apply_downloaded_update).
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ReleaseRepo is the GitHub "owner/name" the Go build updates from. Set it at
// build time with -ldflags "-X silverspoon/internal/update.ReleaseRepo=owner/name".
// Empty disables the check.
var ReleaseRepo = ""

// CheckInterval is the minimum time between automatic checks.
const CheckInterval = 86400 * time.Second

var versionPattern = regexp.MustCompile(`(?i)^v?(\d+)\.(\d+)\.(\d+)(.*)$`)

// Key is the sortable form of a version string.
type Key struct {
	Major, Minor, Patch int
	HasSuffix           bool
	Suffix              string
}

// VersionKey parses "v1.2.3[suffix]". A suffix sorts above no suffix,
// matching the Python app (patch + suffix > patch).
func VersionKey(v string) (Key, bool) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return Key{}, false
	}
	maj, err1 := strconv.Atoi(m[1])
	min, err2 := strconv.Atoi(m[2])
	pat, err3 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return Key{}, false
	}
	return Key{maj, min, pat, m[4] != "", strings.ToLower(m[4])}, true
}

func (a Key) less(b Key) bool {
	switch {
	case a.Major != b.Major:
		return a.Major < b.Major
	case a.Minor != b.Minor:
		return a.Minor < b.Minor
	case a.Patch != b.Patch:
		return a.Patch < b.Patch
	case a.HasSuffix != b.HasSuffix:
		return !a.HasSuffix
	default:
		return a.Suffix < b.Suffix
	}
}

// IsNewerVersion reports whether latest > current; unparseable versions are
// never newer.
func IsNewerVersion(latest, current string) bool {
	l, ok1 := VersionKey(latest)
	c, ok2 := VersionKey(current)
	return ok1 && ok2 && c.less(l)
}

// Release is what an available update looks like to the UI.
type Release struct {
	Version     string
	Changelog   string
	DownloadURL string
}

type ghRelease struct {
	TagName string  `json:"tag_name"`
	Body    *string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// PickAsset returns the first asset named like "SilverSpoon*.zip".
func pickAsset(r ghRelease) string {
	for _, a := range r.Assets {
		if strings.Contains(a.Name, "SilverSpoon") && strings.HasSuffix(a.Name, ".zip") {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// Check asks GitHub for the latest release of repo. It returns (nil, nil)
// when there is no newer release with a usable zip.
func Check(client *http.Client, repo, currentVersion string) (*Release, error) {
	if repo == "" {
		return nil, fmt.Errorf("no release repository configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SilverSpoon-Updater/"+currentVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP Error %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	var data ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return evaluate(data, currentVersion), nil
}

func evaluate(data ghRelease, currentVersion string) *Release {
	if data.TagName == "" || !IsNewerVersion(data.TagName, currentVersion) {
		return nil
	}
	url := pickAsset(data)
	if url == "" {
		return nil
	}
	body := "No changelog provided."
	if data.Body != nil {
		body = *data.Body
	}
	return &Release{Version: data.TagName, Changelog: body, DownloadURL: url}
}
