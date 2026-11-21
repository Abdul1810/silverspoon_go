package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	xdraw "golang.org/x/image/draw"

	"silverspoon/assets"
	"silverspoon/internal/engine"
	"silverspoon/internal/platform"
	"silverspoon/internal/scheduler"
	"silverspoon/internal/solver"
	"silverspoon/internal/store"
	"silverspoon/internal/update"
)

// Links shown in the Help and About menus.
const (
	RepoURL    = "https://github.com/billysams21/SilverSpoon"
	IssuesURL  = "https://github.com/billysams21/SilverSpoon/issues"
	DonateURL  = "https://ko-fi.com/billysm23"
	statusTick = 500 * time.Millisecond
)

// App is the main window and everything on it.
type App struct {
	w   *app.Window
	th  *Theme
	eng *engine.Engine
	sv  *solver.Solver

	settingsMu sync.Mutex
	settings   *store.Settings

	table       *Table
	graph       *SpeedGraph
	clock       ScheduleClock
	sh          *shell
	autoExtract widget.Bool
	lastSaveDir string
	logo        paint.ImageOp

	menu     *Menu
	dialogs  []*Dialog
	uiq      chan func()
	mouse    image.Point
	winSize  image.Point
	views    []engine.View
	lastTick time.Time

	splash      paint.ImageOp
	splashUntil time.Time
	pendingCopy string

	startup []func()
}

// New builds the app over a loaded engine and settings.
func New(eng *engine.Engine, sv *solver.Solver, settings *store.Settings) *App {
	a := &App{eng: eng, sv: sv, settings: settings, uiq: make(chan func(), 64), sh: newShell()}
	a.th = NewTheme(settings.Theme)
	a.table = NewTable(settings.ColumnWidths)
	a.graph = NewSpeedGraph(35)
	a.autoExtract.Value = settings.ExtractAfterDownload
	eng.AutoExtract.Store(a.autoExtract.Value)
	a.views = eng.Views()
	a.table.CollapseFinished(a.views)
	a.table.Rebuild(a.views)
	if img, err := png.Decode(bytes.NewReader(assets.Logo)); err == nil {
		a.splash = paint.NewImageOp(scaleImage(img, 360))
		a.logo = paint.NewImageOp(scaleImage(img, 64))
	}
	a.table.OnAction = a.rowAction
	return a
}

func scaleImage(src image.Image, size int) image.Image {
	b := src.Bounds()
	if b.Dx() <= size && b.Dy() <= size {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size*b.Dy()/b.Dx()))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

// Settings returns a copy of the current settings for other goroutines.
func (a *App) Settings() store.Settings {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	return *a.settings
}

func (a *App) saveSettings(f func(s *store.Settings)) {
	a.settingsMu.Lock()
	if f != nil {
		f(a.settings)
	}
	s := a.settings
	err := store.SaveSettings(s)
	a.settingsMu.Unlock()
	if err != nil {
		engine.Log.Error("Failed to save settings: " + err.Error())
	}
}

// post runs f on the UI goroutine at the next frame.
func (a *App) post(f func()) {
	a.uiq <- f
	if a.w != nil {
		a.w.Invalidate()
	}
}

// OnStartup queues f to run once the window shows (welcome, pending update).
func (a *App) OnStartup(delay time.Duration, f func()) {
	go func() {
		time.Sleep(delay)
		a.post(f)
	}()
}

// Run opens the window and blocks until it closes.
func (a *App) Run(version string) error {
	a.w = new(app.Window)
	a.w.Option(app.Title("SilverSpoon "+version), app.Size(unit.Dp(1080), unit.Dp(700)), app.MinSize(unit.Dp(860), unit.Dp(560)))
	a.splashUntil = time.Now().Add(time.Second)

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(statusTick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.w.Invalidate()
			}
		}
	}()
	a.startScheduler(stop)

	var ops op.Ops
	for {
		switch e := a.w.Event().(type) {
		case app.DestroyEvent:
			a.shutdown()
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.frame(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (a *App) shutdown() {
	a.eng.PauseAllForClose(4 * time.Second)
	a.saveSettings(func(s *store.Settings) { s.ColumnWidths = a.table.SaveWidths() })
	platform.AllowSleep()
	if a.sv != nil {
		a.sv.Stop()
	}
	a.eng.Close()
}

func (a *App) drainQueue() {
	for {
		select {
		case f := <-a.uiq:
			f()
		default:
			return
		}
	}
}

// tick refreshes the task snapshot and speed graph every 500 ms (update_ui).
func (a *App) tick(now time.Time) {
	if now.Sub(a.lastTick) < statusTick-20*time.Millisecond {
		return
	}
	a.lastTick = now
	a.views = a.eng.Views()
	speed, downloading := 0.0, false
	for _, v := range a.views {
		if v.Status == engine.StDownloading {
			speed += v.Speed
			downloading = true
		}
	}
	if downloading || speed > 0 {
		a.graph.Add(speed)
	}
	a.checkUpdateTaskDone()
}

func (a *App) frame(gtx C) {
	a.drainQueue()
	now := time.Now()
	a.tick(now)
	a.winSize = gtx.Constraints.Max
	a.th.Theme.Palette.Bg = a.th.P.Window
	paint.Fill(gtx.Ops, a.th.P.Window)

	if now.Before(a.splashUntil) {
		a.layoutSplash(gtx)
		gtx.Execute(op.InvalidateCmd{At: a.splashUntil})
		return
	}
	if len(a.startup) > 0 {
		for _, f := range a.startup {
			f()
		}
		a.startup = nil
	}

	if a.pendingCopy != "" {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(a.pendingCopy))})
		a.pendingCopy = ""
	}

	a.globalKeys(gtx)
	for a.table.EmptyAction.Clicked(gtx) {
		a.openAddSheet("")
	}
	visible := a.filtered()
	a.table.Rebuild(visible)
	if a.sh.filter == viewAll && strings.TrimSpace(a.sh.search.Text()) == "" {
		a.table.EmptyTitle, a.table.EmptyText = "No downloads yet", "Paste links from FuckingFast, DataNodes, FileKeeper, BZZHR or any direct URL."
	} else {
		a.table.EmptyTitle, a.table.EmptyText = "Nothing here", "No downloads match this view or search."
	}
	layout.Flex{}.Layout(gtx,
		layout.Rigid(a.layoutSidebar),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(a.layoutToolbar),
				layout.Flexed(1, func(gtx C) D {
					size := gtx.Constraints.Max
					d := a.th.Table(gtx, a.table, a.tableKey)
					if a.table.MenuRequested {
						a.table.MenuRequested = false
						a.openContextMenu()
					}
					if n := len(a.selected()); n > 0 {
						m := op.Record(gtx.Ops)
						bg := gtx
						bg.Constraints = layout.Constraints{Max: size}
						sd := a.layoutSelectionBar(bg, n)
						call := m.Stop()
						st := op.Offset(image.Pt((size.X-sd.Size.X)/2, size.Y-sd.Size.Y-gtx.Dp(14))).Push(gtx.Ops)
						call.Add(gtx.Ops)
						st.Pop()
					}
					return d
				}),
				layout.Rigid(a.layoutStatusBar),
			)
		}),
	)

	// Tooltips (under menus and dialogs).
	if len(a.dialogs) == 0 && (a.menu == nil || !a.menu.open) {
		if tip := a.table.HoverTooltip(); tip != "" {
			a.th.Tooltip(gtx, a.mouse, a.winSize, tip)
		}
	}
	if a.menu != nil && a.menu.open {
		a.th.layoutMenu(gtx, a.menu)
	}
	if n := len(a.dialogs); n > 0 {
		d := a.dialogs[n-1]
		a.th.layoutDialog(gtx, d)
		if d.closed {
			a.removeDialog(d)
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	a.trackPointer(gtx)
}

func (a *App) removeDialog(d *Dialog) {
	for i, x := range a.dialogs {
		if x == d {
			a.dialogs = append(a.dialogs[:i], a.dialogs[i+1:]...)
			return
		}
	}
}

// trackPointer keeps the pointer's window position for tooltips and menus.
func (a *App) trackPointer(gtx C) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &a.mouse, Kinds: pointer.Move | pointer.Press | pointer.Drag | pointer.Enter | pointer.Leave})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			a.mouse = e.Position.Round()
			if e.Kind == pointer.Leave {
				a.table.ClearHover()
			}
		}
	}
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &a.mouse)
}

func (a *App) layoutSplash(gtx C) {
	layout.Center.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				if a.splash.Size().X == 0 {
					return D{}
				}
				img := widget.Image{Src: a.splash, Scale: 1.0 / gtx.Metric.PxPerDp}
				return img.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx C) D {
				l := a.th.Label(unit.Sp(20), "SilverSpoon", a.th.P.Text)
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
		)
	})
}

// --- menu bar ------------------------------------------------------------------

// --- top cards -------------------------------------------------------------------

func (a *App) counts() (active, queued, done int, speed float64) {
	for _, v := range a.views {
		switch v.Status {
		case engine.StDownloading, engine.StStarting, engine.StSolving, engine.StWaitingUser, engine.StExtracting:
			active++
		case engine.StQueued, engine.StPending:
			queued++
		case engine.StCompleted, engine.StExtracted, engine.StExtractCanceled:
			done++
		}
		if v.Status == engine.StDownloading {
			speed += v.Speed
		}
	}
	return
}

// --- action bar --------------------------------------------------------------------

func (a *App) refresh() {
	a.views = a.eng.Views()
	if a.w != nil {
		a.w.Invalidate()
	}
}

func (a *App) selected() []string { return a.table.SelectedUIDs(a.filtered()) }

func (a *App) tableKey(name key.Name) {
	switch name {
	case key.NameDeleteForward, key.NameDeleteBackward:
		a.deleteSelected()
	case "F":
		a.forceRedownload()
	case "S":
		a.startSelected()
	case "P":
		a.pauseSelected()
	case "C":
		a.cancelSelected()
	case "R":
		a.retrySelected()
	case key.NameSpace:
		sel := a.selected()
		if len(sel) == 0 {
			return
		}
		if v, ok := a.eng.View(sel[0]); ok && (v.Status == engine.StDownloading || v.Status == engine.StStarting) {
			a.pauseSelected()
		} else {
			a.startSelected()
		}
	}
}

func (a *App) openContextMenu() {
	sel := a.selected()
	items := []*MenuItem{
		{Label: "Start / Resume", Hint: "S", Action: a.startSelected},
		{Label: "Pause", Hint: "P", Action: a.pauseSelected},
		{Label: "Cancel", Hint: "C", Action: a.cancelSelected},
		Sep(),
		Item("Extract Now", a.extractSelected),
		Item("Open Folder", a.openSelectedFolder),
	}
	for _, uid := range sel {
		if v, ok := a.eng.View(uid); ok && v.IsUpdate && v.Status == engine.StCompleted {
			items = append(items, Item("Install Update", a.installSelectedUpdate))
			break
		}
	}
	items = append(items,
		Sep(),
		&MenuItem{Label: "Retry", Hint: "R", Action: a.retrySelected},
		&MenuItem{Label: "Force Redownload", Hint: "F", Action: a.forceRedownload},
		Item("Copy Error Details", a.copyErrorDetails),
		Sep(),
		Item("Schedule download at specific interval", a.scheduleSelected),
		Sep(),
		&MenuItem{Label: "Delete", Hint: "Del", Action: a.deleteSelected},
	)
	if len(sel) == 0 {
		for _, it := range items {
			it.Disabled = it.Label != ""
		}
	}
	a.menu = &Menu{Items: items, At: a.mouse, open: true}
}

// --- actions -------------------------------------------------------------------------

func (a *App) toggleSelectAll() { a.table.ToggleAll(a.filtered()) }

func (a *App) startSelected() { a.eng.StartTasks(a.selected()); a.refresh() }

func (a *App) pauseSelected() { a.eng.PauseTasks(a.selected()); a.refresh() }

func (a *App) cancelSelected() { a.eng.CancelTasks(a.selected()); a.refresh() }

func (a *App) retrySelected() { a.eng.RetryTasks(a.selected()); a.refresh() }

func (a *App) switchTheme(name string) {
	a.th.SetName(name)
	a.saveSettings(func(s *store.Settings) { s.Theme = name })
}

var hrefRE = regexp.MustCompile(`href=['"]?([^'" >]+)`)

// linksFromClipboard prefers links in copied HTML (e.g. a table copied from a
// web page), falling back to plain text.
func linksFromClipboard(html, plain string) string {
	if html != "" {
		var out []string
		for _, m := range hrefRE.FindAllStringSubmatch(html, -1) {
			if strings.HasPrefix(m[1], "http") {
				out = append(out, m[1])
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "\n")
		}
	}
	return plain
}

func (a *App) saveDir() string {
	if a.lastSaveDir != "" {
		return a.lastSaveDir
	}
	return absDir(a.Settings().DefaultSaveDir)
}

func (a *App) forceRedownload() {
	sel := a.selected()
	if len(sel) == 0 {
		a.message("No Selection", "Select one or more tasks to force redownload.")
		return
	}
	run := func() {
		q, s, f := a.eng.ForceRedownload(sel)
		a.refresh()
		if s > 0 || f > 0 || q == 0 {
			a.message("Force Redownload", fmt.Sprintf("Queued: %d\nSkipped active tasks: %d\nFailed: %d", q, s, f))
		}
	}
	if n := a.eng.NeedsRedownloadConfirm(sel); n > 0 {
		a.confirm("Confirm Force Redownload",
			fmt.Sprintf("You have selected %d file(s) that already have download progress.\n\n"+
				"Forcing a redownload will PERMANENTLY DELETE the partially downloaded file(s) and start from 0%%.\n\n"+
				"Are you sure you want to proceed?", n), "Redownload", true, run)
		return
	}
	run()
}

func (a *App) deleteSelected() {
	sel := a.selected()
	if len(sel) == 0 {
		return
	}
	if a.Settings().SkipDeleteConfirmation {
		a.eng.DeleteTasks(sel, false)
		a.refresh()
		return
	}
	var files, dontAsk widget.Bool
	a.dialogs = append(a.dialogs, &Dialog{Title: "Confirm Delete", Width: 440, Default: 0, Cancel: 1,
		Body: func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(a.th.Label(unit.Sp(13), fmt.Sprintf("Are you sure you want to delete %d selected task(s)?", len(sel)), a.th.P.Text).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx C) D { return a.th.CheckBox(gtx, &files, "Also delete downloaded files from disk") }),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx C) D { return a.th.CheckBox(gtx, &dontAsk, "Don't ask again") }),
			)
		},
		Buttons: []*DialogButton{
			Btn("Delete", KindDanger, func() bool {
				if dontAsk.Value {
					a.saveSettings(func(s *store.Settings) { s.SkipDeleteConfirmation = true })
				}
				a.eng.DeleteTasks(sel, files.Value)
				a.refresh()
				return true
			}),
			Btn("Cancel", KindNormal, nil),
		}})
}

func (a *App) openSelectedFolder() {
	sel := a.selected()
	if len(sel) == 0 {
		return
	}
	v, ok := a.eng.View(sel[0])
	if !ok {
		return
	}
	if _, err := os.Stat(v.SaveDir); err != nil {
		a.message("Folder Not Found", "The folder does not exist yet:\n"+v.SaveDir)
		return
	}
	if err := platform.Open(v.SaveDir); err != nil {
		a.message("Error", "Could not open folder:\n"+err.Error())
	}
}

func (a *App) extractSelected() {
	sel := a.selected()
	if len(sel) == 0 {
		a.message("No Selection", "Select one or more tasks to extract.")
		return
	}
	folders := a.eng.ExtractFolders(sel)
	go func() {
		// Re-extracting overwrites what's on disk, so confirm it first.
		var already, fresh []string
		for _, f := range folders {
			if a.eng.AlreadyExtracted(f) {
				already = append(already, f)
			} else {
				fresh = append(fresh, f)
			}
		}
		a.post(func() {
			a.eng.ExtractNow(fresh)
			if len(already) == 0 {
				a.refresh()
				return
			}
			shown := ""
			for i, n := range already {
				if i == 10 {
					shown += fmt.Sprintf("  … and %d more\n", len(already)-10)
					break
				}
				shown += "  • " + n + "\n"
			}
			a.confirm("Already Extracted", "These are already extracted:\n\n"+shown+
				"\nExtract them again? Existing files will be overwritten.", "Extract Again", false,
				func() { a.eng.ExtractNow(already); a.refresh() })
		})
	}()
}

func (a *App) copyErrorDetails() {
	for _, uid := range a.selected() {
		v, ok := a.eng.View(uid)
		if ok && strings.Contains(v.Status, "Error") {
			a.copyErrorLog(v)
			return
		}
	}
	a.message("No Error Selected", "Select a failed task first, then copy its error details.")
}

func (a *App) copyErrorLog(v engine.View) {
	logPath := store.LogPath()
	var lines []string
	found := false
	for _, p := range []string{logPath + ".1", logPath} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		found = true
		lines = append(lines, strings.SplitAfter(string(b), "\n")...)
	}
	if !found {
		a.message("No Log", "No error log found.")
		return
	}
	parts := strings.Split(strings.TrimRight(v.Link, "/"), "/")
	fileID := strings.Split(parts[len(parts)-1], "#")[0]
	var errLines, matching []string
	for _, l := range lines {
		if l == "" || strings.Contains(l, " - INFO - ") {
			continue
		}
		errLines = append(errLines, l)
		for _, k := range []string{v.Link, fileID, v.Filename} {
			if k != "" && strings.Contains(l, k) {
				matching = append(matching, l)
				break
			}
		}
	}
	last20 := func(s []string) string {
		if len(s) > 20 {
			s = s[len(s)-20:]
		}
		return strings.Join(s, "")
	}
	relevant, label := last20(errLines), "Recent error logs"
	if len(matching) > 0 {
		relevant, label = last20(matching), "Matching error logs"
	}
	if strings.TrimSpace(relevant) == "" {
		if v.ErrorMessage == "" {
			a.message("Log Empty", "No error log details found for this task.")
			return
		}
		relevant, label = "Error: "+v.ErrorMessage+"\n", "Error details"
	}
	detail := fmt.Sprintf("Task File: %s\nTask Link: %s\nStatus: %s", v.Filename, v.Link, v.Status)
	if v.ErrorMessage != "" {
		detail += "\nError Detail: " + v.ErrorMessage
	}
	a.pendingCopy = detail + "\n\n" + label + ":\n" + relevant
	a.message("Log Copied", "Relevant error logs have been copied to your clipboard.")
}

// --- scheduler wiring -------------------------------------------------------------

func (a *App) startScheduler(stop chan struct{}) {
	if s := a.Settings().Schedule; s != nil {
		a.eng.SetSchedule(*s)
	} else {
		a.eng.SetSchedule(scheduler.DefaultSchedule())
	}
	onClose := func(sum scheduler.Summary, path string, saved bool) {
		a.post(func() { a.showOffpeakSummary(sum, path, saved) })
	}
	go func() {
		// A first poll shortly after launch, so a wake-launched app (or one
		// opened mid-window) starts without waiting a full interval.
		time.Sleep(1500 * time.Millisecond)
		a.eng.SchedulerTick(time.Now(), onClose)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				a.eng.SchedulerTick(now, onClose)
				a.w.Invalidate()
			}
		}
	}()
	a.refreshWakeTask()
}

// LaunchCommand is what the wake task runs to relaunch the app.
func LaunchCommand() (string, string) {
	exe, _ := os.Executable()
	return exe, "--background"
}

// refreshWakeTask re-registers an enabled wake task on startup so it uses
// the current executable path.
func (a *App) refreshWakeTask() {
	s := a.Settings().Schedule
	if runtime.GOOS != "windows" || s == nil || !s.Enabled || !s.WakeTimer {
		return
	}
	sched := *s
	go func() {
		exe, args := LaunchCommand()
		if ok, msg := scheduler.RegisterWakeTask(sched, exe, args); !ok {
			engine.Log.Error("Could not refresh the wake timer task: " + msg)
		}
	}()
}

func (a *App) scheduleSelected() {
	sel := a.selected()
	if len(sel) == 0 {
		a.message("No Selection", "Select one or more downloads to schedule.")
		return
	}
	label := fmt.Sprintf("%d selected download(s)", len(sel))
	if len(sel) == 1 {
		if v, ok := a.eng.View(sel[0]); ok {
			label = v.Filename
		}
	}
	a.openScheduler(sel, label)
}

func (a *App) showOffpeakSummary(s scheduler.Summary, path string, saved bool) {
	report := "Note: the report file could not be written."
	if saved {
		report = "Report appended to " + path
	}
	a.message("Download Scheduler — Summary", fmt.Sprintf(
		"Scheduled download window finished.\n\nFiles completed: %d\nDownloaded: %.2f GB\n"+
			"Active duration: %.0f min\nAverage speed: %.2f MB/s\nPeak speed: %.2f MB/s\n\n%s",
		s.FilesCompleted, float64(s.BytesDownloaded)/(1<<30), s.DurationSeconds/60,
		s.AvgSpeedMbps, s.PeakSpeedMbps, report))
}

// --- simple dialogs ------------------------------------------------------------------

func (a *App) textBody(txt string) func(gtx C) D {
	return func(gtx C) D { return a.th.Label(unit.Sp(13), txt, a.th.P.Text).Layout(gtx) }
}

// message shows an OK box.
func (a *App) message(title, txt string) {
	a.dialogs = append(a.dialogs, &Dialog{Title: title, Body: a.textBody(txt), Default: 0, Cancel: 0,
		Buttons: []*DialogButton{Btn("OK", KindPrimary, nil)}})
	if a.w != nil {
		a.w.Invalidate()
	}
}

// confirm asks a yes/no question; yes runs on confirmation. danger styles
// the confirm button red.
func (a *App) confirm(title, txt, yesLabel string, danger bool, yes func()) {
	kind := KindPrimary
	if danger {
		kind = KindDanger
	}
	a.dialogs = append(a.dialogs, &Dialog{Title: title, Body: a.textBody(txt), Default: -1, Cancel: 1,
		Buttons: []*DialogButton{
			Btn(yesLabel, kind, func() bool { yes(); return true }),
			Btn("Cancel", KindNormal, nil),
		}})
}

// richBody lays out paragraphs; lines starting with "# " are headings and
// "• " bullets.
func (a *App) richBody(txt string) func(gtx C) D {
	lines := strings.Split(txt, "\n")
	return func(gtx C) D {
		children := make([]layout.FlexChild, 0, len(lines))
		for _, l := range lines {
			l := l
			children = append(children, layout.Rigid(func(gtx C) D {
				switch {
				case strings.HasPrefix(l, "# "):
					return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
						return Bold(a.th.Label(unit.Sp(14), l[2:], a.th.P.Text)).Layout(gtx)
					})
				case strings.HasPrefix(l, "• "):
					return layout.Inset{Left: unit.Dp(10), Bottom: unit.Dp(2)}.Layout(gtx,
						a.th.Label(unit.Sp(13), l, a.th.P.Text).Layout)
				case l == "":
					return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx)
				}
				return a.th.Label(unit.Sp(13), l, a.th.P.Text).Layout(gtx)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

func (a *App) showContributing() {
	a.dialogs = append(a.dialogs, &Dialog{Title: "Contributing Guide", Default: 0, Cancel: 0,
		Body: a.richBody("# Contributing to SilverSpoon\nWe welcome contributions! Please see the CONTRIBUTING.md file in the repository for full details.\n\n# Quick Rules\n" +
			"• Always work on the dev branch.\n• Carefully test your changes before submitting a PR.\n• Report bugs via the GitHub Issues tab."),
		Buttons: []*DialogButton{Btn("OK", KindPrimary, nil)}})
}

func (a *App) showAbout() {
	a.dialogs = append(a.dialogs, &Dialog{Title: "About SilverSpoon", Width: 520, Default: 0, Cancel: 0,
		Body: a.richBody("# SilverSpoon " + engine.Version + "\n" +
			"A simple, fast bulk downloader for FuckingFast, DataNodes, FileKeeper and BZZHR links, and any direct download URL.\n" +
			"Select your links, paste them in, and hit Add!\nLicensed under the GNU GPLv3.\n\n" +
			"# What's new in the Go edition\n" +
			"• Rewritten in Go: one small native binary, lower memory use, no Python runtime.\n" +
			"• Same queue, settings and progress files as the Python app, so existing downloads resume.\n" +
			"• Keyboard navigation in the queue (arrows, Shift-select, Ctrl+A, ←/→ to fold batches).\n" +
			"• Resizable columns, an empty-queue hint and clearer dialogs.\n\n" +
			"# Carried over from v1.6.0\n" +
			"• Multi-connection downloads, FileKeeper and BZZHR support.\n" +
			"• Extraction progress and cancel, and skipping archives already extracted.\n" +
			"• Updates download through the normal queue; install now or on next open.\n" +
			"• Only one copy of SilverSpoon runs at a time."),
		Buttons: []*DialogButton{Btn("OK", KindPrimary, nil)}})
}

func (a *App) showWelcome(manual bool) {
	var dont widget.Bool
	dont.Value = manual && !a.Settings().ShowWarningDialog
	a.dialogs = append(a.dialogs, &Dialog{Title: "Welcome to SilverSpoon!", Width: 520, Default: 0, Cancel: 0,
		Body: func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(a.richBody("# Keyboard Shortcuts\n"+
					"• S or Space — Start / Resume selected downloads\n"+
					"• P or Space — Pause selected downloads\n"+
					"• C — Cancel selected downloads\n"+
					"• R — Retry failed downloads\n"+
					"• F — Force Redownload selected tasks\n"+
					"• Delete or Backspace — Delete selected tasks\n"+
					"• ↑ ↓ (Shift to extend), Ctrl+A, ← → to fold batches\n\n"+
					"# Tips & Troubleshooting")),
				layout.Rigid(func(gtx C) D {
					return background(gtx, unit.Dp(6), withAlpha(a.th.P.Accent, 0x1a), a.th.P.Accent,
						layout.UniformInset(unit.Dp(10)), a.richBody(
							"• Slow speeds: try changing your DNS (e.g. Cloudflare 1.1.1.1 or Google 8.8.8.8) or enabling a VPN.\n"+
								"• CAPTCHA: Cloudflare checks are solved automatically in the background. If a browser window pops up asking you to verify you're human, complete it and the download will continue."))
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx C) D { return a.th.CheckBox(gtx, &dont, "Don't show this again") }),
			)
		},
		Buttons: []*DialogButton{Btn("OK", KindPrimary, func() bool {
			a.saveSettings(func(s *store.Settings) { s.ShowWarningDialog = !dont.Value })
			return true
		})}})
}

// ShowWelcomeIfEnabled queues the welcome dialog for first launch.
func (a *App) ShowWelcomeIfEnabled() {
	if a.Settings().ShowWarningDialog {
		a.OnStartup(1100*time.Millisecond, func() { a.showWelcome(false) })
	}
}

// --- updates ---------------------------------------------------------------------------

// StartupUpdateCheck runs the daily check (Windows only, like the Python
// app's frozen build) and offers a deferred install.
func (a *App) StartupUpdateCheck() {
	a.OnStartup(2200*time.Millisecond, a.checkPendingUpdate)
	if runtime.GOOS == "windows" && update.ReleaseRepo != "" {
		a.checkForUpdates(false)
	}
}

func (a *App) checkForUpdates(force bool) {
	if update.ReleaseRepo == "" {
		if force {
			a.message("Update Check Failed", "Could not check for updates:\nNo release repository is configured for this build.")
		}
		return
	}
	last := a.Settings().LastUpdateCheck
	now := float64(time.Now().Unix())
	if !force && now-last < update.CheckInterval.Seconds() {
		return
	}
	go func() {
		rel, err := update.Check(nil, update.ReleaseRepo, engine.Version)
		a.saveSettings(func(s *store.Settings) { s.LastUpdateCheck = now })
		a.post(func() {
			switch {
			case err != nil:
				if force {
					a.message("Update Check Failed", "Could not check for updates:\n"+err.Error())
				}
			case rel == nil:
				if force {
					a.message("Up to date", "You are already using the latest version of SilverSpoon!")
				}
			default:
				a.promptUpdate(rel)
			}
		})
	}()
}

func (a *App) promptUpdate(rel *update.Release) {
	exe, _ := os.Executable()
	if !update.CanWriteAppDir(exe) {
		a.message("Update Available (Admin Required)", fmt.Sprintf("Version %s is available!\n\n"+
			"However, SilverSpoon is located in a protected folder:\n%s\n\n"+
			"Please run SilverSpoon as Administrator to update automatically, or move it to a normal folder like Downloads or Desktop.",
			rel.Version, filepath.Dir(exe)))
		return
	}
	// Don't queue the same version twice, unless the earlier try failed or
	// its file is gone.
	for _, v := range a.eng.Views() {
		if v.IsUpdate && v.UpdateVersion == rel.Version && v.Status != engine.StError && v.Status != engine.StCancelled {
			if _, err := os.Stat(v.FilePath); err == nil {
				return
			}
		}
	}
	folder := "SilverSpoon Update " + rel.Version
	uid := a.eng.AddUpdateTask(rel.DownloadURL, a.saveDir(), folder, rel.Version)
	a.table.SelectUIDs([]string{uid})
	a.refresh()
	a.dialogs = append(a.dialogs, &Dialog{Title: "Update Available: " + rel.Version, Width: 560, Default: 0, Cancel: 1,
		Body: a.richBody("# A new version (" + rel.Version + ") is available!\n" +
			"Added to your download queue as \"" + folder + "\". Start it now or later — you can pause and resume it like any other download.\n\n" +
			"# Changelog\n" + rel.Changelog),
		Buttons: []*DialogButton{
			Btn("Start download now", KindPrimary, func() bool { a.eng.StartTasks([]string{uid}); a.refresh(); return true }),
			Btn("I'll start it later", KindNormal, nil),
		}})
}

func (a *App) checkUpdateTaskDone() {
	pending := ""
	if p := a.Settings().PendingUpdate; p != nil {
		pending = p.Version
	}
	for _, v := range a.views {
		if v.IsUpdate && v.Status == engine.StCompleted && !v.InstallHandled && v.UpdateVersion != pending {
			if _, err := os.Stat(v.FilePath); err != nil {
				continue
			}
			a.eng.SetInstallHandled(v.UID)
			// Save now, not debounced: "Install now" quits the app.
			a.eng.SaveNow()
			a.promptInstall(v)
		}
	}
}

func (a *App) installSelectedUpdate() {
	for _, uid := range a.selected() {
		if v, ok := a.eng.View(uid); ok && v.IsUpdate && v.Status == engine.StCompleted {
			if _, err := os.Stat(v.FilePath); err == nil {
				a.promptInstall(v)
				return
			}
		}
	}
	a.message("No Update Selected", "Select a completed SilverSpoon update download first.")
}

func (a *App) promptInstall(v engine.View) {
	a.dialogs = append(a.dialogs, &Dialog{Title: "Update Downloaded", Default: 0, Cancel: 2,
		Body: a.textBody(fmt.Sprintf("SilverSpoon %s has finished downloading.\n\nInstall it now (the app will restart), or on the next launch?", v.UpdateVersion)),
		Buttons: []*DialogButton{
			Btn("Install now", KindPrimary, func() bool { a.applyUpdate(v.FilePath); return true }),
			Btn("Install on next open", KindNormal, func() bool {
				a.saveSettings(func(s *store.Settings) {
					s.PendingUpdate = &store.PendingUpdate{Zip: v.FilePath, Version: v.UpdateVersion}
				})
				a.message("Update Scheduled", fmt.Sprintf("SilverSpoon %s will be installed the next time you open the app.", v.UpdateVersion))
				return true
			}),
			Btn("Not yet", KindNormal, nil),
		}})
}

func (a *App) checkPendingUpdate() {
	p := a.Settings().PendingUpdate
	if p == nil {
		return
	}
	if _, err := os.Stat(p.Zip); p.Zip == "" || err != nil {
		a.saveSettings(func(s *store.Settings) { s.PendingUpdate = nil })
		return
	}
	zip := p.Zip
	a.confirm("Install Update", fmt.Sprintf("SilverSpoon %s was downloaded earlier and is ready to install.\n\nInstall it now? The app will restart.", p.Version),
		"Install", false, func() { a.applyUpdate(zip) })
}

func (a *App) applyUpdate(zip string) {
	a.saveSettings(func(s *store.Settings) { s.PendingUpdate = nil })
	a.eng.SaveNow()
	err := update.Apply(zip)
	if err == nil {
		a.saveSettings(func(s *store.Settings) { s.ColumnWidths = a.table.SaveWidths() })
		os.Exit(0)
	}
	if e, ok := err.(*update.ErrNotSilverSpoon); ok {
		a.dialogs = append(a.dialogs, &Dialog{Title: "Update Downloaded (Manual Action Required)", Default: 1, Cancel: 1,
			Body: a.textBody(e.Error()),
			Buttons: []*DialogButton{
				Btn("Copy Directory Path", KindNormal, func() bool { a.pendingCopy = e.ExtractDir; return true }),
				Btn("OK", KindPrimary, nil),
			}})
		return
	}
	a.message("Update Failed", "Failed to apply the update:\n"+err.Error())
}

var _ = text.Start
