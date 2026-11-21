package ui

import (
	"image"
	"os"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"silverspoon/internal/engine"
	"silverspoon/internal/store"
)

// harness drives real frames through Gio's input router, so clicks and
// keys reach the same handlers as in the window.
type harness struct {
	t   *testing.T
	a   *App
	r   input.Router
	ops op.Ops
}

func newHarness(t *testing.T) *harness {
	old := store.Dir
	store.Dir = t.TempDir()
	t.Cleanup(func() { store.Dir = old })
	recs := []store.TaskRecord{}
	for i, st := range []string{engine.StCompleted, engine.StPaused, engine.StError} {
		recs = append(recs, store.TaskRecord{UID: "u" + itoa(i), Link: "https://example.com/Game.part" + itoa(i+1) + ".rar",
			BaseSaveDir: t.TempDir(), FolderName: "Game", Status: st, ErrorMessage: "boom", TotalBytes: 100, DownloadedBytes: 50, Progress: 50})
	}
	eng := engine.New(recs, func() engine.Config { return engine.Config{MaxWorkers: 1, MaxConnections: 1} }, nil)
	s := store.Defaults()
	s.ShowWarningDialog = false
	a := New(eng, nil, s)
	a.splashUntil = time.Time{}
	a.table.collapsed["Game"] = false
	h := &harness{t: t, a: a}
	h.frame()
	return h
}

func (h *harness) frame() {
	h.ops.Reset()
	gtx := layout.Context{
		Ops:         &h.ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(1100, 700)),
		Now:         time.Now(),
		Source:      h.r.Source(),
	}
	h.a.lastTick = time.Time{}
	h.a.frame(gtx)
	h.r.Frame(&h.ops)
}

func (h *harness) click(x, y float32, buttons pointer.Buttons, mods key.Modifiers) {
	p := f32.Pt(x, y)
	h.r.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: buttons, Position: p, Modifiers: mods},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p, Modifiers: mods},
	)
	h.frame()
	h.frame()
}

// Layout at 1 px/dp: sidebar 232 wide, toolbar 64 high, batch row 46,
// task rows 52 each.
const (
	listX  = 232 + 400
	batchY = 64 + 23
	task0Y = 64 + 46 + 26
	task1Y = task0Y + 52
)

func TestClickSelectsRowAndShowsSelectionBar(t *testing.T) {
	h := newHarness(t)
	h.click(listX, task0Y, pointer.ButtonPrimary, 0)
	if got := h.a.selected(); len(got) != 1 || got[0] != "u0" {
		t.Fatalf("selected %v", got)
	}
	// Ctrl-click adds the second task.
	h.click(listX, task1Y, pointer.ButtonPrimary, key.ModShortcut)
	if got := h.a.selected(); len(got) != 2 {
		t.Fatalf("ctrl-click selected %v", got)
	}
	// Clicking the batch row selects all its tasks.
	h.click(listX, batchY, pointer.ButtonPrimary, 0)
	if got := h.a.selected(); len(got) != 3 {
		t.Fatalf("batch click selected %v", got)
	}
}

func TestRightClickOpensContextMenu(t *testing.T) {
	h := newHarness(t)
	h.click(listX, task1Y, pointer.ButtonSecondary, 0)
	if h.a.menu == nil || !h.a.menu.open {
		t.Fatal("context menu did not open")
	}
	if got := h.a.selected(); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("right click selected %v", got)
	}
}

func TestKeyboardShortcutsActOnSelection(t *testing.T) {
	h := newHarness(t)
	h.click(listX, task1Y, pointer.ButtonPrimary, 0) // the paused task
	h.r.Queue(key.Event{Name: "S", State: key.Press})
	h.frame()
	h.frame()
	if v, _ := h.a.eng.View("u1"); v.Status != engine.StPending {
		t.Fatalf("S did not start: %q", v.Status)
	}
}

func TestSidebarFiltersAndAddSheet(t *testing.T) {
	h := newHarness(t)
	h.a.setFilter(viewErrors)
	h.frame()
	if f := h.a.filtered(); len(f) != 1 || f[0].UID != "u2" {
		t.Fatalf("errors view %v", f)
	}
	h.a.setFilter(viewAll)
	h.frame()
	// The toolbar's Add button (right edge) opens the sheet.
	h.click(1100-18-60, 32, pointer.ButtonPrimary, 0)
	if len(h.a.dialogs) != 1 || h.a.dialogs[0].Title != "Add downloads" {
		t.Fatalf("dialogs %d", len(h.a.dialogs))
	}
	// Escape closes it.
	h.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	h.frame()
	if len(h.a.dialogs) != 0 {
		t.Fatal("Escape did not close the sheet")
	}
}

func TestHostSummaryAndMeta(t *testing.T) {
	got := hostSummary([]string{"https://fuckingfast.co/a", "https://fuckingfast.co/b", "https://filekeeper.net/x", "https://x.com/f.zip"})
	if got != "FuckingFast ×2, FileKeeper ×1, Direct ×1" {
		t.Errorf("%q", got)
	}
	r := Row{Size: "1.0 MB of 2.0 MB", Speed: "1.50 MB/s", ETA: "3s"}
	if r.Meta() != "1.0 MB of 2.0 MB  ·  1.50 MB/s  ·  3s left" {
		t.Errorf("%q", r.Meta())
	}
	if taskView(engine.StCaptchaTimeout) != viewErrors || taskView(engine.StPaused) != viewQueued ||
		taskView(engine.StExtracting) != viewActive || taskView(engine.StExtracted) != viewDone {
		t.Error("taskView mapping")
	}
}

// TestLiveFolderExpandsInAddSheet opens the sheet with a real DataNodes
// folder link and waits for it to turn into file links (reads the page
// only): SILVERSPOON_LIVE_FOLDER=<url> go test -run LiveFolder ./internal/ui
func TestLiveFolderExpandsInAddSheet(t *testing.T) {
	link := os.Getenv("SILVERSPOON_LIVE_FOLDER")
	if link == "" {
		t.Skip("set SILVERSPOON_LIVE_FOLDER")
	}
	h := newHarness(t)
	h.a.openAddSheet(link)
	var body string
	for i := 0; i < 200; i++ {
		h.frame()
		time.Sleep(50 * time.Millisecond)
		if len(h.a.dialogs) == 0 {
			t.Fatal("sheet closed")
		}
		body = sheetLinks(h)
		if !strings.Contains(body, "/f/") && strings.Count(body, "\n") > 0 {
			break
		}
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	t.Logf("%d links, first %s, last %s", len(lines), lines[0], lines[len(lines)-1])
	if len(lines) < 2 || strings.Contains(body, "/f/") {
		t.Fatalf("folder not expanded:\n%s", body)
	}
}

// sheetLinks returns the Add sheet's links text by rendering its body and
// reading the first editor laid out (the links box).
func sheetLinks(h *harness) string { return h.a.addSheetLinks() }
