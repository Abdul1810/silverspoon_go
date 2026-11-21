package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"silverspoon/internal/engine"
)

// Row is one line of the queue table: a batch folder or a task inside one.
type Row struct {
	Key      string // "f:<folder>" or "t:<uid>"
	IsBatch  bool
	Folder   string
	UID      string // tasks only
	Name     string
	Status   string
	Progress float64 // bar value (extraction progress while extracting)
	ProgText string
	Speed    string
	ETA      string
	Size     string
	Tooltip  string
	Children int
}

// FormatETA renders seconds as "1h 2m", "3m 4s" or "5s".
func FormatETA(seconds float64) string {
	if seconds <= 0 || seconds > 1e12 {
		return "-"
	}
	s := int(seconds)
	m, s := s/60, s%60
	h, m := m/60, m%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

const mb = 1024 * 1024

// HumanBytes renders a byte count as "512 KB", "500.0 MB" or "39.53 GB".
func HumanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func sizeText(done, total int64) string {
	if total <= 0 {
		return "-"
	}
	if done >= total {
		return HumanBytes(total)
	}
	return HumanBytes(done) + " of " + HumanBytes(total)
}

// Meta is a task's secondary line: size · speed · time left.
func (r Row) Meta() string {
	parts := []string{}
	if r.Size != "-" {
		parts = append(parts, r.Size)
	} else {
		parts = append(parts, "Size unknown")
	}
	if r.Speed != "-" {
		parts = append(parts, r.Speed)
	}
	if r.ETA != "-" {
		parts = append(parts, r.ETA+" left")
	}
	return strings.Join(parts, "  ·  ")
}

// BatchMeta summarises a folder: files · size · speed · time left.
func (r Row) BatchMeta() string {
	files := fmt.Sprintf("%d files", r.Children)
	if r.Children == 1 {
		files = "1 file"
	}
	parts := []string{files}
	if r.Size != "-" {
		parts = append(parts, r.Size)
	}
	if r.ProgText != "" && r.ProgText != "-" {
		parts = append(parts, r.ProgText)
	}
	if r.Speed != "-" {
		parts = append(parts, r.Speed)
	}
	if r.ETA != "-" {
		parts = append(parts, r.ETA+" left")
	}
	return strings.Join(parts, "  ·  ")
}

// wrap breaks s into lines of at most width runes at spaces.
func wrap(s string, width int) string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		if cur != "" && len([]rune(cur))+1+len([]rune(w)) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur == "" {
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

func taskRow(v engine.View) Row {
	r := Row{Key: "t:" + v.UID, Folder: v.FolderName, UID: v.UID, Name: v.Filename, Status: v.Status}
	extracting := v.Status == engine.StExtracting
	switch {
	case extracting && v.HasExtractProgress:
		r.ProgText = fmt.Sprintf("%.1f%%", v.ExtractProgress)
	case extracting:
		r.ProgText = "Extracting..."
	case v.Status == engine.StExtracted || v.Status == "Extract Error":
		r.ProgText = "--"
	default:
		r.ProgText = fmt.Sprintf("%.1f%%", v.Progress)
	}
	r.Progress = v.Progress
	if extracting {
		r.Progress = v.ExtractProgress
	}
	r.Speed, r.ETA = "-", "-"
	if v.Status == engine.StDownloading {
		r.Speed = fmt.Sprintf("%.2f MB/s", v.Speed)
		if v.Speed > 0 && v.TotalBytes > 0 {
			r.ETA = FormatETA(float64(v.TotalBytes-v.DownloadedBytes) / (v.Speed * mb))
		}
	}
	r.Size = sizeText(v.DownloadedBytes, v.TotalBytes)
	switch {
	case strings.Contains(v.Status, "Error") && v.ErrorMessage != "":
		r.Tooltip = wrap(v.ErrorMessage, 60)
	case v.Status == engine.StWaitingUser:
		r.Tooltip = `Complete the "Verify you are human" check in the browser window that opened.`
	}
	return r
}

// batchRow aggregates a folder (update_ui's batch logic).
func batchRow(folder string, ts []engine.View) Row {
	var dl, size int64
	var speed float64
	allDone, anyErr, anyDl := true, false, false
	var extracting []float64
	var hasExtract []bool
	var largest int64
	for _, t := range ts {
		if t.TotalBytes > largest {
			largest = t.TotalBytes
		}
	}
	isArchive := func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".rar" || ext == ".zip"
	}
	for _, t := range ts {
		if t.TotalBytes > 0 {
			dl += t.DownloadedBytes
			size += t.TotalBytes
		} else if isArchive(t.Filename) {
			// Estimate unknown parts by the largest known file.
			size += largest
		}
		speed += t.Speed
		switch t.Status {
		case engine.StCompleted, engine.StExtracted, engine.StExtracting, engine.StExtractCanceled:
		default:
			allDone = false
		}
		if t.Status == engine.StExtracting {
			extracting = append(extracting, t.ExtractProgress)
			hasExtract = append(hasExtract, t.HasExtractProgress)
		}
		if strings.Contains(t.Status, "Error") {
			anyErr = true
		}
		switch t.Status {
		case engine.StDownloading, engine.StStarting, engine.StPending:
			anyDl = true
		}
	}
	status := engine.StQueued
	switch {
	case allDone && len(extracting) > 0:
		status = engine.StExtracting
	case allDone:
		status = engine.StCompleted
	case anyErr:
		status = "Contains Errors"
	case anyDl:
		status = "Active"
	}
	prog := 0.0
	if size > 0 {
		prog = float64(dl) / float64(size) * 100
	}
	progText := fmt.Sprintf("%.1f%%", prog)
	if status == engine.StExtracting {
		var sum float64
		n := 0
		for i, p := range extracting {
			if hasExtract[i] {
				sum += p
				n++
			}
		}
		prog, progText = 0, "-"
		if n > 0 {
			prog = sum / float64(n)
			progText = fmt.Sprintf("%.1f%%", prog)
		}
	}
	r := Row{Key: "f:" + folder, IsBatch: true, Folder: folder, Name: folder, Status: status,
		Progress: prog, ProgText: progText, Speed: "-", ETA: "-", Size: sizeText(dl, size), Children: len(ts)}
	if speed > 0 {
		r.Speed = fmt.Sprintf("%.2f MB/s", speed)
	}
	if anyDl && speed > 0 && size > 0 {
		var remaining int64
		for _, t := range ts {
			if t.TotalBytes > 0 {
				remaining += t.TotalBytes - t.DownloadedBytes
			} else if isArchive(t.Filename) {
				remaining += largest
			}
		}
		if remaining > 0 {
			r.ETA = FormatETA(float64(remaining) / (speed * mb))
		}
	}
	return r
}

// group splits views into folders, in order of first appearance.
func group(views []engine.View) ([]string, map[string][]engine.View) {
	var order []string
	m := map[string][]engine.View{}
	for _, v := range views {
		if _, ok := m[v.FolderName]; !ok {
			order = append(order, v.FolderName)
		}
		m[v.FolderName] = append(m[v.FolderName], v)
	}
	return order, m
}

// tagKind picks the pill colours for a status.
func tagKind(p *Palette, status string) TagColors {
	switch {
	case strings.Contains(status, "Error") || status == "Contains Errors" || status == engine.StCaptchaTimeout:
		return p.TagError
	case status == engine.StCompleted || status == engine.StExtracted:
		return p.TagDone
	case status == engine.StDownloading || status == "Active" || status == engine.StExtracting ||
		status == engine.StSolving || status == engine.StWaitingUser || status == engine.StStarting:
		return p.TagActive
	case status == engine.StPaused || status == engine.StPausing || status == engine.StCancelled ||
		status == engine.StExtractCanceled:
		return p.TagPaused
	}
	return p.TagOther
}

// barColors picks the progress gradient for a status.
func barColors(p *Palette, status string) [2]colorT {
	switch {
	case status == engine.StExtracting:
		return p.BarExtract
	case status == engine.StCompleted || status == engine.StExtracted || status == engine.StExtractCanceled:
		return p.BarDone
	case strings.Contains(status, "Error") || status == "Contains Errors":
		return p.BarError
	}
	return p.BarActive
}
