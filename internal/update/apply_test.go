package update

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnzipAndFindApp(t *testing.T) {
	d := t.TempDir()
	zp := filepath.Join(d, "u.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	for _, n := range []string{"SilverSpoon/SilverSpoon.exe", "SilverSpoon/7z.exe"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	f.Close()
	out := filepath.Join(d, "out")
	if err := unzip(zp, out); err != nil {
		t.Fatal(err)
	}
	if got := findApp(out); got != filepath.Join(out, "SilverSpoon") {
		t.Errorf("findApp = %q", got)
	}
}

func TestUnzipRejectsTraversal(t *testing.T) {
	d := t.TempDir()
	zp := filepath.Join(d, "bad.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../evil.txt")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if err := unzip(zp, filepath.Join(d, "out")); err == nil {
		t.Error("traversal accepted")
	}
}

func TestScriptRollsBackAndCleansUp(t *testing.T) {
	s := Script(`C:\Apps\SilverSpoon`, `C:\T\x\SilverSpoon`, `C:\Apps\SilverSpoon\SilverSpoon.exe`, `C:\T\m.marker`, `C:\T\x`, `C:\D\u.zip`)
	for _, want := range []string{
		`move "C:\Apps\SilverSpoon" "C:\Apps\SilverSpoon.previous"`,
		`robocopy "C:\T\x\SilverSpoon" "C:\Apps\SilverSpoon" /E`,
		`set "SILVERSPOON_UPDATE_SUCCESS_MARKER=C:\T\m.marker"`,
		"for /l %%i in (1,1,30) do (",
		`move "C:\Apps\SilverSpoon.previous" "C:\Apps\SilverSpoon"`,
		`del "%~f0"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}
