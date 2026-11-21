package update

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotSilverSpoon means the running executable isn't named SilverSpoon*,
// so the automatic swap is refused; ExtractDir says where the files are.
type ErrNotSilverSpoon struct{ ExtractDir string }

func (e *ErrNotSilverSpoon) Error() string {
	return "The update has been downloaded and extracted to:\n" + e.ExtractDir + "\n\n" +
		"Because you are running SilverSpoon from a differently named executable, " +
		"the automatic replacement was aborted to keep you safe."
}

func unzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	root, _ := filepath.Abs(dest)
	for _, f := range r.File {
		p := filepath.Join(root, f.Name)
		if !strings.HasPrefix(p, root+string(filepath.Separator)) && p != root {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(p, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode()|0o200)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// findApp returns the directory inside dir holding SilverSpoon.exe.
func findApp(dir string) string {
	found := ""
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && found == "" && !d.IsDir() && strings.EqualFold(d.Name(), "SilverSpoon.exe") {
			found = filepath.Dir(p)
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// CanWriteAppDir reports whether the folder holding exe is writable.
func CanWriteAppDir(exe string) bool {
	test := filepath.Join(filepath.Dir(exe), ".update_test_permission")
	if err := os.WriteFile(test, []byte("test"), 0o644); err != nil {
		return !errors.Is(err, os.ErrPermission)
	}
	os.Remove(test)
	return true
}

// Script is the batch file that swaps the app directory, starts the new
// build, waits for its success marker and rolls back otherwise.
func Script(appDir, newDir, exe, marker, extractDir, zipPath string) string {
	backup := appDir + ".previous"
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\r\n", a...) }
	w("@echo off")
	w("echo Updating SilverSpoon...")
	w(`del /f /q "%s" > nul 2>&1`, marker)
	w(`if exist "%s" rmdir /s /q "%s"`, backup, backup)
	w(":wait_for_exit")
	w(`move "%s" "%s" > nul 2>&1`, appDir, backup)
	w("if not errorlevel 1 goto install")
	w("timeout /t 1 /nobreak > nul")
	w("goto wait_for_exit")
	w(":install")
	w(`robocopy "%s" "%s" /E /COPY:DAT /R:3 /W:1 > nul`, newDir, appDir)
	w("if errorlevel 8 goto rollback")
	w(`set "SILVERSPOON_UPDATE_SUCCESS_MARKER=%s"`, marker)
	w(`start "" "%s"`, exe)
	w("for /l %%%%i in (1,1,30) do (")
	w(`    if exist "%s" goto success`, marker)
	w("    timeout /t 1 /nobreak > nul")
	w(")")
	w(":rollback")
	w(`if exist "%s" rmdir /s /q "%s"`, appDir, appDir)
	w(`move "%s" "%s" > nul 2>&1`, backup, appDir)
	w(`if exist "%s" start "" "%s"`, exe, exe)
	w("goto cleanup")
	w(":success")
	w(`rmdir /s /q "%s" > nul 2>&1`, backup)
	w(":cleanup")
	w(`del /f /q "%s" > nul 2>&1`, marker)
	w(`rmdir /s /q "%s" > nul 2>&1`, extractDir)
	w(`del /q "%s" > nul 2>&1`, zipPath)
	w(`del "%%~f0"`)
	return b.String()
}

// Apply extracts zipPath and starts the swap script. On success the caller
// must save state and exit right away so the script can move the folder.
func Apply(zipPath string) error {
	if _, err := os.Stat(zipPath); err != nil {
		return errors.New("The downloaded update file is missing. Please download it again.")
	}
	stamp := time.Now().Unix()
	tmp := os.TempDir()
	extractDir := filepath.Join(tmp, fmt.Sprintf("silverspoon_extract_%d", stamp))
	if err := unzip(zipPath, extractDir); err != nil {
		return err
	}
	newDir := findApp(extractDir)
	if newDir == "" {
		return errors.New("Could not find SilverSpoon.exe inside the downloaded zip.")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.ToLower(filepath.Base(exe)), "silverspoon") {
		return &ErrNotSilverSpoon{ExtractDir: extractDir}
	}
	marker := filepath.Join(tmp, fmt.Sprintf("silverspoon_update_success_%d.marker", stamp))
	bat := filepath.Join(tmp, fmt.Sprintf("silverspoon_update_%d.bat", stamp))
	if err := os.WriteFile(bat, []byte(Script(filepath.Dir(exe), newDir, exe, marker, extractDir, zipPath)), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/c", bat)
	// The script must not hold the app folder as its working directory, or
	// Windows won't let it rename that folder.
	cmd.Dir = tmp
	hideConsole(cmd)
	return cmd.Start()
}
