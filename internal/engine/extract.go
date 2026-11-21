package engine

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"silverspoon/internal/extraction"
	"silverspoon/internal/solver"
)

func cookieList(cookies map[string]string) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(cookies))
	for k, v := range cookies {
		out = append(out, &http.Cookie{Name: k, Value: v})
	}
	return out
}

var (
	firstPart = regexp.MustCompile(`(?i)\.part0*1\.rar$`)
	seq001    = regexp.MustCompile(`\.001$`)
	anyPart   = regexp.MustCompile(`(?i)\.part\d+\.rar$`)
)

// ArchiveVolumes lists the first volume of each archive in saveDir, i.e.
// what the extractor is given.
func ArchiveVolumes(saveDir string) ([]string, error) {
	entries, err := os.ReadDir(saveDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	var vols []string
	for _, f := range files {
		low := strings.ToLower(f)
		switch {
		case firstPart.MatchString(f), seq001.MatchString(f):
			vols = append(vols, filepath.Join(saveDir, f))
		case (strings.HasSuffix(low, ".rar") || strings.HasSuffix(low, ".zip")) && !anyPart.MatchString(f):
			vols = append(vols, filepath.Join(saveDir, f))
		}
	}
	if len(vols) == 0 && len(files) > 0 {
		vols = append(vols, filepath.Join(saveDir, files[0]))
	}
	return vols, nil
}

func which(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// FindExtractor returns (type, command, sevenZip). type is "" when nothing is
// installed. sevenZip reads archive listings, also when WinRAR extracts.
func FindExtractor() (kind, cmd, sevenZip string) {
	bundled := ""
	if runtime.GOOS == "windows" {
		// Windows: prefer installed 7-Zip > WinRAR > bundled 7z.exe
		for _, p := range []string{filepath.Join(solver.AppDir, "7z.exe"), filepath.Join(solver.AppDir, "assets", "7z.exe")} {
			if exists(p) {
				bundled = p
				break
			}
		}
		installed7z := `C:\Program Files\7-Zip\7z.exe`
		winrar := `C:\Program Files\WinRAR\WinRAR.exe`
		switch {
		case exists(installed7z):
			kind, cmd = "7z", installed7z
		case exists(winrar):
			kind, cmd = "winrar", winrar
		case bundled != "":
			kind, cmd = "7z", bundled
		}
	} else {
		if p := which("7z", "7zz", "7za"); p != "" {
			kind, cmd = "7z", p
		} else if p := which("unrar"); p != "" {
			kind, cmd = "unrar", p
		}
	}
	switch {
	case kind == "7z":
		sevenZip = cmd
	case bundled != "":
		sevenZip = bundled
	default:
		sevenZip = which("7z", "7zz", "7za")
	}
	return
}

// folders groups tasks by batch folder, in queue order. Lock held.
func (e *Engine) folders() ([]string, map[string][]*Task) {
	var order []string
	m := map[string][]*Task{}
	for _, t := range e.tasks {
		if _, ok := m[t.FolderName]; !ok {
			order = append(order, t.FolderName)
		}
		m[t.FolderName] = append(m[t.FolderName], t)
	}
	return order, m
}

func (e *Engine) checkExtraction() {
	e.mu.Lock()
	defer e.mu.Unlock()
	order, groups := e.folders()
	for _, name := range order {
		ts := groups[name]
		if e.extracted[name] {
			continue
		}
		// Never auto-extract an app-update download.
		upd, allValid, allExtracted, anyExtracting := false, true, true, false
		for _, t := range ts {
			upd = upd || t.IsUpdate
			switch t.Status {
			case StCompleted, StExtracted, StExtracting:
			default:
				allValid = false
			}
			if t.Status != StExtracted {
				allExtracted = false
			}
			if t.Status == StExtracting {
				anyExtracting = true
			}
		}
		if upd || len(ts) == 0 || !allValid {
			continue
		}
		if allExtracted {
			e.extracted[name] = true
			continue
		}
		// If any task in this folder is extracting, don't start another.
		if anyExtracting {
			continue
		}
		e.extracted[name] = true
		go e.extractFolder(append([]*Task(nil), ts...), false)
	}
}

// AlreadyExtracted reports whether the folder is marked Extracted, or every
// file in its archives is already on disk with the right size.
func (e *Engine) AlreadyExtracted(folder string) bool {
	e.mu.Lock()
	_, groups := e.folders()
	ts := groups[folder]
	all := len(ts) > 0
	saveDir := ""
	for _, t := range ts {
		if t.Status != StExtracted {
			all = false
		}
		saveDir = t.SaveDir
	}
	e.mu.Unlock()
	if all {
		return true
	}
	if saveDir == "" {
		return false
	}
	vols, err := ArchiveVolumes(saveDir)
	if err != nil {
		return false
	}
	_, _, sz := FindExtractor()
	if len(vols) == 0 || sz == "" {
		return false
	}
	for _, v := range vols {
		entries := extraction.ListArchive(sz, v)
		if len(entries) == 0 || !extraction.IsExtracted(entries, saveDir) {
			return false
		}
	}
	return true
}

// ExtractFolders returns the batch folders of the selected tasks that can be
// extracted now (none of them is already extracting).
func (e *Engine) ExtractFolders(uids []string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var order []string
	seen := map[string]bool{}
	busy := map[string]bool{}
	for _, t := range e.pick(uids) {
		if !seen[t.FolderName] {
			seen[t.FolderName] = true
			order = append(order, t.FolderName)
		}
		if t.Status == StExtracting {
			busy[t.FolderName] = true
		}
	}
	var out []string
	for _, f := range order {
		if !busy[f] {
			out = append(out, f)
		}
	}
	return out
}

// ExtractNow extracts the folders, even over existing files.
func (e *Engine) ExtractNow(folders []string) {
	e.mu.Lock()
	_, groups := e.folders()
	for _, f := range folders {
		e.extracted[f] = true
		go e.extractFolder(append([]*Task(nil), groups[f]...), true)
	}
	e.mu.Unlock()
}

func (e *Engine) extractFolder(ts []*Task, force bool) {
	if len(ts) == 0 {
		return
	}
	var saveDir, folder string
	e.update(func() {
		saveDir, folder = ts[0].SaveDir, ts[0].FolderName
		for _, t := range ts {
			t.Status, t.ExtractProgress, t.CancelFlag = StExtracting, nil, false
		}
	})
	// Cancel (or Delete) on any task of the folder stops its extraction.
	shouldStop := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, t := range ts {
			if t.CancelFlag {
				return true
			}
		}
		return false
	}
	setAll := func(status, msg string) {
		e.update(func() {
			for _, t := range ts {
				t.Status, t.ErrorMessage = status, msg
			}
		})
	}
	unmark := func() {
		e.update(func() { delete(e.extracted, folder) })
	}

	vols, _ := ArchiveVolumes(saveDir)
	if len(vols) == 0 {
		setAll("Extract Error (No File)", fmt.Sprintf("No archive file was found in %s.", saveDir))
		unmark()
		return
	}
	kind, base, sz := FindExtractor()
	if kind == "" {
		setAll("Extract Error (No extractor found)", "No supported extractor was found. Install 7-Zip or WinRAR, then retry extraction.")
		unmark()
		return
	}
	listings := make([][]extraction.Entry, len(vols))
	for i, v := range vols {
		if sz != "" {
			listings[i] = extraction.ListArchive(sz, v)
		}
	}
	// Auto-extract skips archives whose files are already all on disk with
	// matching sizes (e.g. extracted earlier, outside the app).
	if !force {
		done := true
		for _, l := range listings {
			if len(l) == 0 || !extraction.IsExtracted(l, saveDir) {
				done = false
			}
		}
		if done {
			e.update(func() {
				for _, t := range ts {
					t.Status, t.ErrorMessage, t.ExtractProgress = StExtracted, "", nil
				}
			})
			e.RequestSave()
			return
		}
	}
	// Weight each archive by its unpacked size so the bar moves evenly.
	weights := make([]float64, len(vols))
	allW := true
	for i, l := range listings {
		for _, en := range l {
			weights[i] += float64(en.Size)
		}
		if weights[i] == 0 {
			allW = false
		}
	}
	if !allW {
		for i := range weights {
			weights[i] = 1
		}
	}
	var totalW, doneW float64
	for _, w := range weights {
		totalW += w
	}

	var runErr error
	var exitCode int
	var tail string
	for i, vol := range vols {
		if shouldStop() {
			runErr = extraction.ErrCancelled
			break
		}
		var cmd []string
		switch kind {
		case "7z":
			cmd = []string{base, "x", vol, "-o" + saveDir, "-y", "-bsp1", "-bso0"}
		case "winrar":
			cmd = []string{base, "x", "-y", vol, saveDir + `\`}
		case "unrar":
			cmd = []string{base, "x", vol, saveDir + "/", "-y"}
		}
		baseW, w := doneW, weights[i]
		report := func(p int) {
			v := (baseW + w*float64(p)/100) / totalW * 100
			e.update(func() {
				for _, t := range ts {
					vv := v
					t.ExtractProgress = &vv
				}
			})
		}
		code, out, err := extraction.RunWithProgress(cmd, report, shouldStop)
		if err != nil {
			runErr = err
			break
		}
		if code != 0 {
			exitCode, tail = code, out
			break
		}
		doneW += w
	}

	switch {
	case errors.Is(runErr, extraction.ErrCancelled):
		// Partly extracted files stay on disk; "Extract Now" redoes it. The
		// folder stays marked so auto-extract won't restart it.
		e.update(func() {
			for _, t := range ts {
				if t.Status == StExtracting { // a deleted task is already "Cancelled"
					t.Status = StExtractCanceled
				}
				t.ExtractProgress, t.CancelFlag = nil, false
			}
		})
	case exitCode != 0:
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		Log.Error(fmt.Sprintf("Extraction error (subprocess): exit status %d\n%s", exitCode, tail))
		setAll("Extract Error (Corrupt?)", fmt.Sprintf("Extractor failed with exit code %d. The archive may be corrupt, incomplete, or password-protected.", exitCode))
		unmark()
	case runErr != nil:
		Log.Error(fmt.Sprintf("Extraction error: %v", runErr))
		setAll("Extract Error", "Extraction failed: "+FormatError(runErr))
		unmark()
	default:
		e.update(func() {
			for _, t := range ts {
				t.Status, t.ErrorMessage, t.ExtractProgress = StExtracted, "", nil
			}
		})
	}
	e.RequestSave()
}
