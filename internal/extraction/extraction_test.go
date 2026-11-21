package extraction

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const listing = `Path = sub
Size = 0
Attributes = D

Path = fg-01.bin
Size = 1200
Attributes = A

Path = sub\setup.bin
Size = 34
Folder = -

Path = folder-only
Folder = +
`

// sevenZip finds the bundled 7z.exe on Windows, or a 7-Zip on PATH.
func sevenZip(t *testing.T) string {
	if runtime.GOOS == "windows" {
		p, _ := filepath.Abs(filepath.Join("..", "..", "assets", "7z.exe"))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, n := range []string{"7z", "7zz", "7za"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("no 7z available")
	return ""
}

func writeRandom(t *testing.T, path string, n int) {
	b := make([]byte, n)
	rand.Read(b)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseListingSkipsFolders(t *testing.T) {
	want := []Entry{{"fg-01.bin", 1200}, {`sub\setup.bin`, 34}}
	if got := ParseListing(listing); !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
	if got := ParseListing(strings.ReplaceAll(listing, "\n", "\r\n")); !reflect.DeepEqual(got, want) {
		t.Errorf("CRLF: %v", got)
	}
	if len(ParseListing("")) != 0 {
		t.Error("empty")
	}
}

func TestIsExtractedComparesSizes(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	entries := []Entry{{"a.bin", 5}, {filepath.Join("sub", "b.bin"), 3}}
	if IsExtracted(entries, d) {
		t.Error("nothing there yet")
	}
	os.WriteFile(filepath.Join(d, "a.bin"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(d, "sub", "b.bin"), []byte("12"), 0o644)
	if IsExtracted(entries, d) {
		t.Error("b.bin is short")
	}
	os.WriteFile(filepath.Join(d, "sub", "b.bin"), []byte("123"), 0o644)
	if !IsExtracted(entries, d) {
		t.Error("should be extracted")
	}
	if IsExtracted(nil, d) {
		t.Error("empty entries")
	}
}

func TestListExtractAndDetectWith7z(t *testing.T) {
	sz := sevenZip(t)
	d := t.TempDir()
	src, out := filepath.Join(d, "src"), filepath.Join(d, "out")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	writeRandom(t, filepath.Join(src, "big.bin"), 3_000_000)
	os.WriteFile(filepath.Join(src, "sub", "small.txt"), []byte("hello"), 0o644)
	// Multi-volume, like a split repack (Game.7z.001, .002, ...).
	cmd := exec.Command(sz, "a", "-mx0", "-v1m", filepath.Join(d, "Game.7z"), ".")
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	first := filepath.Join(d, "Game.7z.001")

	entries := ListArchive(sz, first)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	want := []Entry{{"big.bin", 3_000_000}, {filepath.Join("sub", "small.txt"), 5}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("%v", entries)
	}
	if ListArchive(sz, filepath.Join(src, "big.bin")) != nil {
		t.Error("not an archive should be nil")
	}
	if IsExtracted(entries, out) {
		t.Error("extracted before extraction")
	}

	var seen []int
	code, _, err := RunWithProgress([]string{sz, "x", first, "-o" + out, "-y", "-bsp1", "-bso0"},
		func(p int) { seen = append(seen, p) }, nil)
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	for _, p := range seen {
		if p < 0 || p > 100 {
			t.Errorf("progress %d", p)
		}
	}
	if !IsExtracted(entries, out) {
		t.Error("not detected as extracted")
	}
	os.Remove(filepath.Join(out, "sub", "small.txt"))
	if IsExtracted(entries, out) {
		t.Error("missing file not detected")
	}
}

func TestRunWithProgressCanBeCancelled(t *testing.T) {
	sz := sevenZip(t)
	d := t.TempDir()
	src := filepath.Join(d, "big.bin")
	writeRandom(t, src, 40_000_000)
	archive := filepath.Join(d, "Game.7z")
	if b, err := exec.Command(sz, "a", "-mx0", archive, src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	out := filepath.Join(d, "out")
	_, _, err := RunWithProgress([]string{sz, "x", archive, "-o" + out, "-y", "-bsp1", "-bso0"},
		func(int) {}, func() bool { return true })
	if err != ErrCancelled {
		t.Fatalf("err = %v", err)
	}
	if IsExtracted([]Entry{{"big.bin", 40_000_000}}, out) {
		t.Error("extracted despite cancel")
	}
}

func TestRunWithProgressReportsFailure(t *testing.T) {
	sz := sevenZip(t)
	code, out, err := RunWithProgress([]string{sz, "x", "does-not-exist.7z", "-y", "-bsp1", "-bso0"}, func(int) {}, nil)
	if err != nil || code == 0 || strings.TrimSpace(out) == "" {
		t.Errorf("code %d out %q err %v", code, out, err)
	}
}
