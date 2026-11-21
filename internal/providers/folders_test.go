package providers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestIsFolderLink(t *testing.T) {
	for link, want := range map[string]bool{
		"https://datanodes.to/f/ues1cut1vhwzc1csjg3iqz66vt42q6z3": true,
		"https://www.datanodes.to/f/abc123/":                      true,
		"https://datanodes.to/cqxp3cg0ntjh/GVE.part01.rar":        false,
		"https://fuckingfast.co/f/abc":                            false,
		"https://datanodes.to/files":                              false,
		"not a url":                                               false,
	} {
		if got := IsFolderLink(link); got != want {
			t.Errorf("IsFolderLink(%q) = %v", link, got)
		}
	}
}

// A mock folder with 25 files over two pages (20 + 5), listed out of order
// and each twice, as the real page does (thumbnail and title link).
func TestExpandFolderPagesDedupesAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		from, to := 0, 0
		switch page {
		case 1:
			from, to = 1, 20
		case 2:
			from, to = 21, 25
		}
		for i := to; i >= from && i > 0; i-- {
			link := fmt.Sprintf("https://datanodes.to/%012d/GVE.part%02d.rar", i, i)
			fmt.Fprintf(w, `<a href="%s" class="thumb"></a><a href="%s">GVE.part%02d.rar</a>`, link, link, i)
		}
		fmt.Fprint(w, `<a href="https://datanodes.to/users/Someone">user</a>`)
	}))
	defer srv.Close()
	got, err := ExpandFolder(srv.URL+"/f/abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 25 {
		t.Fatalf("%d links", len(got))
	}
	if FilenameFromLink(got[0]) != "GVE.part01.rar" || FilenameFromLink(got[9]) != "GVE.part10.rar" || FilenameFromLink(got[24]) != "GVE.part25.rar" {
		t.Errorf("order: %s %s %s", got[0], got[9], got[24])
	}
}

func TestExpandFolderEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html></html>") }))
	defer srv.Close()
	if _, err := ExpandFolder(srv.URL+"/f/abc", nil); err == nil {
		t.Error("no error for an empty folder")
	}
}

func TestNaturalLess(t *testing.T) {
	in := []string{"a.part10.rar", "a.part2.rar", "a.part1.rar", "setup.exe", "a.part02b.rar"}
	got := append([]string(nil), in...)
	for i := range got {
		for j := i + 1; j < len(got); j++ {
			if naturalLess(got[j], got[i]) {
				got[i], got[j] = got[j], got[i]
			}
		}
	}
	want := []string{"a.part1.rar", "a.part2.rar", "a.part02b.rar", "a.part10.rar", "setup.exe"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
}

// TestLiveDataNodesFolder reads the user's folder link (no downloading):
// SILVERSPOON_LIVE_FOLDER=<url> go test -run LiveDataNodes ./internal/providers
func TestLiveDataNodesFolder(t *testing.T) {
	link := os.Getenv("SILVERSPOON_LIVE_FOLDER")
	if link == "" {
		t.Skip("set SILVERSPOON_LIVE_FOLDER")
	}
	got, err := ExpandFolder(link, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		t.Log(l)
	}
}
