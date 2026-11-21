package update

import (
	"encoding/json"
	"testing"
)

func TestIsNewerVersion(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.6.1", "v1.6.0", true},
		{"1.7.0", "v1.6.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.6.0", "v1.6.0", false},
		{"v1.5.9", "v1.6.0", false},
		{"v1.6.0-hotfix", "v1.6.0", true}, // suffix sorts above no suffix
		{"v1.6.0", "v1.6.0-hotfix", false},
		{"v1.6.0b", "v1.6.0a", true},
		{"V1.6.1", "v1.6.0", true},
		{"latest", "v1.6.0", false},
		{"v1.6.1", "dev", false},
		{" v1.6.1 ", "v1.6.0", true},
	} {
		if got := IsNewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("IsNewerVersion(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

func TestEvaluatePicksFirstSilverSpoonZip(t *testing.T) {
	raw := `{"tag_name":"v2.1.0","body":"notes","assets":[
		{"name":"checksums.txt","browser_download_url":"u0"},
		{"name":"SilverSpoon-linux.tar.gz","browser_download_url":"u1"},
		{"name":"SilverSpoon-windows.zip","browser_download_url":"u2"},
		{"name":"SilverSpoon-other.zip","browser_download_url":"u3"}]}`
	var r ghRelease
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	rel := evaluate(r, "v2.0.0")
	if rel == nil || rel.DownloadURL != "u2" || rel.Version != "v2.1.0" || rel.Changelog != "notes" {
		t.Fatalf("%+v", rel)
	}
	if evaluate(r, "v2.1.0") != nil {
		t.Error("same version offered")
	}
	r.Assets = r.Assets[:2]
	if evaluate(r, "v2.0.0") != nil {
		t.Error("offered a release with no zip")
	}
	r.Assets = nil
	r.Body = nil
	r.Assets = append(r.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{"SilverSpoon.zip", "u"})
	if rel := evaluate(r, "v2.0.0"); rel == nil || rel.Changelog != "No changelog provided." {
		t.Errorf("%+v", rel)
	}
}
