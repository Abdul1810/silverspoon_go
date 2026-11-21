package ui

import (
	"image"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"silverspoon/internal/engine"
	"silverspoon/internal/platform"
)

func editorStyle(th *Theme, ed *widget.Editor, hint string) material.EditorStyle {
	e := material.Editor(th.Theme, ed, hint)
	e.Color, e.HintColor = th.P.Text, th.P.TextSubtle
	e.SelectionColor = withAlpha(th.P.Selection, 0x70)
	e.TextSize = unit.Sp(13)
	return e
}

func openURL(u string) { platform.Open(u) }

func clipboardText() (html, plain string) { return platform.ReadClipboard() }

func pickFolder(start string) (string, bool) {
	return platform.PickFolder("Select Save Directory", start)
}

func readPickedFile() (string, bool) {
	p, ok := platform.PickFile("Import Links")
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func absDir(d string) string {
	d = strings.TrimSpace(d)
	if abs, err := filepath.Abs(d); err == nil {
		return abs
	}
	return d
}

func joinPath(dir, name string) string {
	if name == "" {
		return dir
	}
	return filepath.Join(dir, name)
}

// Switch is an on/off toggle with a label.
func (t *Theme) Switch(gtx C, b *widget.Bool, label string) D {
	return b.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(t.Label(unit.Sp(12), label, t.P.TextMuted).Layout),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx C) D {
				w, h := gtx.Dp(30), gtx.Dp(17)
				track := t.P.BorderStrong
				if b.Value {
					track = t.P.Accent
				}
				box(gtx, image.Rect(0, 0, w, h), h/2, track, colorT{})
				k := h - gtx.Dp(4)
				x := gtx.Dp(2)
				if b.Value {
					x = w - k - gtx.Dp(2)
				}
				box(gtx, image.Rect(x, gtx.Dp(2), x+k, gtx.Dp(2)+k), k/2, rgb(0xffffff), colorT{})
				pointerCursor(gtx, image.Pt(w, h), pointer.CursorPointer)
				return D{Size: image.Pt(w, h)}
			}),
		)
	})
}

// globalKeys are window-wide shortcuts that work when no text field has
// focus: Ctrl+N adds downloads, Ctrl+V pastes links into a new sheet,
// Ctrl+F searches, Ctrl+, opens settings.
func (a *App) globalKeys(gtx C) {
	if len(a.dialogs) > 0 {
		return
	}
	for _, e := range keyPressed(gtx,
		key.Filter{Name: "N", Required: key.ModShortcut},
		key.Filter{Name: "F", Required: key.ModShortcut},
		key.Filter{Name: ",", Required: key.ModShortcut},
		key.Filter{Focus: a.table, Name: "V", Required: key.ModShortcut},
	) {
		switch e.Name {
		case "N":
			a.openAddSheet("")
		case "F":
			gtx.Execute(key.FocusCmd{Tag: &a.sh.search})
		case ",":
			a.openSettings()
		case "V":
			go func() {
				html, plain := clipboardText()
				txt := linksFromClipboard(html, plain)
				a.post(func() { a.openAddSheet(txt) })
			}()
		}
	}
}

// rowAction handles the inline buttons on a list row.
func (a *App) rowAction(r Row, act RowAction) {
	var uids []string
	if r.IsBatch {
		for _, v := range a.filtered() {
			if v.FolderName == r.Folder {
				uids = append(uids, v.UID)
			}
		}
	} else {
		uids = []string{r.UID}
	}
	switch act {
	case ActToggle:
		switch r.Status {
		case engine.StDownloading, engine.StStarting, "Active", engine.StPending, engine.StSolving, engine.StWaitingUser:
			a.eng.PauseTasks(uids)
		default:
			a.eng.StartTasks(uids)
		}
		a.refresh()
	case ActFolder:
		if len(uids) == 0 {
			return
		}
		if v, ok := a.eng.View(uids[0]); ok {
			if _, err := os.Stat(v.SaveDir); err != nil {
				a.message("Folder Not Found", "The folder does not exist yet:\n"+v.SaveDir)
				return
			}
			platform.Open(v.SaveDir)
		}
	}
}

// vcenter lays w out at its natural height, centred vertically in the
// available height and given the full width.
func vcenter(gtx C, w layout.Widget) D {
	size := gtx.Constraints.Max
	gtx.Constraints.Min = image.Pt(size.X, 0)
	m := op.Record(gtx.Ops)
	d := w(gtx)
	call := m.Stop()
	st := op.Offset(image.Pt(0, (size.Y-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	st.Pop()
	return D{Size: size}
}
