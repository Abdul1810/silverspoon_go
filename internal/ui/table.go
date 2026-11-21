package ui

import (
	"image"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"silverspoon/internal/engine"
)

const (
	taskRowHeight  = unit.Dp(52)
	batchRowHeight = unit.Dp(46)
)

// RowAction is an inline row button.
type RowAction int

const (
	ActToggle RowAction = iota // start or pause
	ActFolder
)

type rowState struct {
	check, toggle, folder, more widget.Clickable
	lastClick                   time.Time
}

// Table is the download list: batch folders with their tasks underneath.
type Table struct {
	list      widget.List
	rows      []Row
	states    map[string]*rowState
	collapsed map[string]bool
	groups    map[string][]engine.View
	widths    map[string]int // column_widths from settings, kept as-is

	selected map[string]bool // row keys
	checked  map[string]bool // task uids
	anchor   string
	cursor   string

	hoverKey string
	hoverTip bool

	// MenuRequested is set on right click or the ⋯ button; the app opens the
	// context menu at the pointer and clears it.
	MenuRequested bool
	// OnAction runs an inline row action on the row's tasks.
	OnAction func(r Row, a RowAction)
	// EmptyAction is the empty state's button.
	EmptyAction           widget.Clickable
	EmptyTitle, EmptyText string
}

// NewTable keeps the saved column widths so settings round-trip unchanged.
func NewTable(widths map[string]int) *Table {
	t := &Table{states: map[string]*rowState{}, collapsed: map[string]bool{},
		selected: map[string]bool{}, checked: map[string]bool{}, widths: widths}
	t.list.Axis = layout.Vertical
	return t
}

// SaveWidths returns the column widths to store.
func (t *Table) SaveWidths() map[string]int {
	if t.widths == nil {
		return map[string]int{}
	}
	return t.widths
}

// CollapseFinished collapses folders whose tasks are all done (on load).
func (t *Table) CollapseFinished(views []engine.View) {
	order, groups := group(views)
	for _, f := range order {
		done := true
		for _, v := range groups[f] {
			switch v.Status {
			case engine.StCompleted, engine.StExtracted, engine.StExtractCanceled:
			default:
				done = false
			}
		}
		t.collapsed[f] = done
	}
}

// Rebuild recomputes the visible rows from the (filtered) tasks.
func (t *Table) Rebuild(views []engine.View) {
	order, groups := group(views)
	rows := t.rows[:0]
	live := map[string]bool{}
	for _, f := range order {
		b := batchRow(f, groups[f])
		rows = append(rows, b)
		live[b.Key] = true
		for _, v := range groups[f] {
			live["t:"+v.UID] = true
			if !t.collapsed[f] {
				rows = append(rows, taskRow(v))
			}
		}
	}
	t.rows, t.groups = rows, groups
	for k := range t.selected {
		if !live[k] {
			delete(t.selected, k)
		}
	}
	for u := range t.checked {
		if !live["t:"+u] {
			delete(t.checked, u)
		}
	}
	for k := range t.states {
		if !live[k] {
			delete(t.states, k)
		}
	}
}

// SelectedUIDs is what actions apply to: the checked tasks, or else the
// tasks under the selected rows.
func (t *Table) SelectedUIDs(views []engine.View) []string {
	var out []string
	for _, v := range views {
		if t.checked[v.UID] {
			out = append(out, v.UID)
		}
	}
	if len(out) > 0 {
		return out
	}
	seen := map[string]bool{}
	for _, v := range views {
		if (t.selected["f:"+v.FolderName] || t.selected["t:"+v.UID]) && !seen[v.UID] {
			seen[v.UID] = true
			out = append(out, v.UID)
		}
	}
	return out
}

// AnyChecked reports whether any task is checked.
func (t *Table) AnyChecked() bool { return len(t.checked) > 0 }

// ClearSelection unchecks and deselects everything.
func (t *Table) ClearSelection() {
	t.checked = map[string]bool{}
	t.selected = map[string]bool{}
}

// ToggleAll checks every task, or clears all when everything is checked.
func (t *Table) ToggleAll(views []engine.View) {
	if len(views) == 0 {
		return
	}
	all := true
	for _, v := range views {
		if !t.checked[v.UID] {
			all = false
		}
	}
	t.checked = map[string]bool{}
	if !all {
		for _, v := range views {
			t.checked[v.UID] = true
		}
	}
}

// SelectUIDs checks the given tasks, e.g. a new update task.
func (t *Table) SelectUIDs(uids []string) {
	for _, u := range uids {
		t.checked[u] = true
	}
}

// Expand opens a batch folder.
func (t *Table) Expand(folder string) { t.collapsed[folder] = false }

func (t *Table) selectionChanged() {
	t.checked = map[string]bool{}
	for f, vs := range t.groups {
		for _, v := range vs {
			if t.selected["f:"+f] || t.selected["t:"+v.UID] {
				t.checked[v.UID] = true
			}
		}
	}
}

func (t *Table) rowIndex(key string) int {
	for i, r := range t.rows {
		if r.Key == key {
			return i
		}
	}
	return -1
}

func (t *Table) clickRow(i int, mods key.Modifiers) {
	k := t.rows[i].Key
	switch {
	case mods.Contain(key.ModShift) && t.anchor != "":
		a := t.rowIndex(t.anchor)
		if a < 0 {
			a = i
		}
		if !mods.Contain(key.ModShortcut) {
			t.selected = map[string]bool{}
		}
		for j := min(a, i); j <= max(a, i); j++ {
			t.selected[t.rows[j].Key] = true
		}
	case mods.Contain(key.ModShortcut):
		if t.selected[k] {
			delete(t.selected, k)
		} else {
			t.selected[k] = true
		}
		t.anchor = k
	default:
		t.selected = map[string]bool{k: true}
		t.anchor = k
	}
	t.cursor = k
	t.selectionChanged()
}

func (t *Table) toggleCheck(r Row) {
	if r.IsBatch {
		all, _ := t.batchCheck(r.Folder)
		for _, v := range t.groups[r.Folder] {
			if all {
				delete(t.checked, v.UID)
			} else {
				t.checked[v.UID] = true
			}
		}
		return
	}
	if t.checked[r.UID] {
		delete(t.checked, r.UID)
	} else {
		t.checked[r.UID] = true
	}
}

func (t *Table) batchCheck(folder string) (all, some bool) {
	vs := t.groups[folder]
	n := 0
	for _, v := range vs {
		if t.checked[v.UID] {
			n++
		}
	}
	return len(vs) > 0 && n == len(vs), n > 0 && n < len(vs)
}

func (t *Table) moveCursor(d int, mods key.Modifiers) {
	if len(t.rows) == 0 {
		return
	}
	i := t.rowIndex(t.cursor)
	if i < 0 {
		i = 0
	} else {
		i = clampInt(i+d, 0, len(t.rows)-1)
	}
	t.clickRow(i, mods&key.ModShift)
	if i < t.list.Position.First {
		t.list.Position.First, t.list.Position.Offset = i, 0
	} else if c := t.list.Position.Count; c > 1 && i >= t.list.Position.First+c-1 {
		t.list.Position.First, t.list.Position.Offset = i-c+2, 0
	}
}

// HoverTooltip returns the tooltip for the hovered row's status.
func (t *Table) HoverTooltip() string {
	if t.hoverKey == "" || !t.hoverTip {
		return ""
	}
	for _, r := range t.rows {
		if r.Key == t.hoverKey {
			return r.Tooltip
		}
	}
	return ""
}

// ClearHover forgets the hovered row.
func (t *Table) ClearHover() { t.hoverKey = "" }

// Table draws the list. onKey receives shortcut keys while it has focus.
func (th *Theme) Table(gtx C, t *Table, onKey func(name key.Name)) D {
	// FocusFilter makes the list focusable, so FocusCmd on click works and
	// the shortcuts below reach it.
	for _, e := range keyPressed(gtx,
		key.FocusFilter{Target: t},
		key.Filter{Focus: t, Name: key.NameUpArrow, Optional: key.ModShift},
		key.Filter{Focus: t, Name: key.NameDownArrow, Optional: key.ModShift},
		key.Filter{Focus: t, Name: key.NameLeftArrow},
		key.Filter{Focus: t, Name: key.NameRightArrow},
		key.Filter{Focus: t, Name: "A", Required: key.ModShortcut},
		key.Filter{Focus: t, Name: key.NameDeleteForward},
		key.Filter{Focus: t, Name: key.NameDeleteBackward},
		key.Filter{Focus: t, Name: "F"}, key.Filter{Focus: t, Name: "S"}, key.Filter{Focus: t, Name: "P"},
		key.Filter{Focus: t, Name: key.NameSpace}, key.Filter{Focus: t, Name: "C"}, key.Filter{Focus: t, Name: "R"},
		key.Filter{Focus: t, Name: key.NameEscape},
	) {
		switch e.Name {
		case key.NameUpArrow:
			t.moveCursor(-1, e.Modifiers)
		case key.NameDownArrow:
			t.moveCursor(1, e.Modifiers)
		case key.NameLeftArrow, key.NameRightArrow:
			if i := t.rowIndex(t.cursor); i >= 0 && t.rows[i].IsBatch {
				t.collapsed[t.rows[i].Folder] = e.Name == key.NameLeftArrow
			}
		case "A":
			t.selected = map[string]bool{}
			for _, r := range t.rows {
				t.selected[r.Key] = true
			}
			t.selectionChanged()
		case key.NameEscape:
			t.ClearSelection()
		default:
			onKey(e.Name)
		}
	}

	size := gtx.Constraints.Max
	th.listBackground(gtx, t)
	if len(t.rows) == 0 {
		th.emptyState(gtx, t)
		return D{Size: size}
	}
	material.List(th.Theme, &t.list).Layout(gtx, len(t.rows), func(gtx C, i int) D {
		return th.listRow(gtx, t, i)
	})
	return D{Size: size}
}

func (th *Theme) listBackground(gtx C, t *Table) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &t.rows, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			gtx.Execute(key.FocusCmd{Tag: t})
			if e.Buttons.Contain(pointer.ButtonPrimary) && !e.Modifiers.Contain(key.ModShortcut) {
				t.ClearSelection()
			}
		}
	}
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &t.rows)
	event.Op(gtx.Ops, t)
}

func (th *Theme) emptyState(gtx C, t *Table) {
	layout.Center.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				s := gtx.Dp(64)
				r := image.Rect(0, 0, s, s)
				box(gtx, r, s/2, th.P.AccentSoft, colorT{})
				st := op.Offset(image.Pt(s/4, s/4)).Push(gtx.Ops)
				IconAt(gtx, icDownload, th.P.AccentText, unit.Dp(32))
				st.Pop()
				return D{Size: r.Max}
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx C) D { return Bold(th.Label(unit.Sp(16), t.EmptyTitle, th.P.Text)).Layout(gtx) }),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(th.Label(unit.Sp(13), t.EmptyText, th.P.TextMuted).Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(18)}.Layout),
			layout.Rigid(func(gtx C) D {
				return th.IconLabelButton(gtx, &t.EmptyAction, icAdd, "Add downloads", th.P.Primary)
			}),
		)
	})
}

func (th *Theme) listRow(gtx C, t *Table, i int) D {
	r := t.rows[i]
	st := t.states[r.Key]
	if st == nil {
		st = &rowState{}
		t.states[r.Key] = st
	}
	h := gtx.Dp(taskRowHeight)
	if r.IsBatch {
		h = gtx.Dp(batchRowHeight)
	}
	w := gtx.Constraints.Max.X
	size := image.Pt(w, h)

	for {
		ev, ok := gtx.Event(pointer.Filter{Target: st, Kinds: pointer.Press | pointer.Enter | pointer.Move | pointer.Leave})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Enter, pointer.Move:
			t.hoverKey = r.Key
			// Tooltips explain the status, which sits on the right.
			t.hoverTip = int(e.Position.X) > w*55/100
		case pointer.Leave:
			if t.hoverKey == r.Key {
				t.hoverKey = ""
			}
		case pointer.Press:
			gtx.Execute(key.FocusCmd{Tag: t})
			if e.Buttons.Contain(pointer.ButtonSecondary) {
				// Right click keeps a multi-selection, or selects the row.
				if !t.selected[r.Key] && !(t.AnyChecked() && (r.IsBatch || t.checked[r.UID])) {
					t.clickRow(i, 0)
				}
				t.MenuRequested = true
				continue
			}
			now := time.Now()
			if now.Sub(st.lastClick) < 400*time.Millisecond && t.selected[r.Key] {
				st.lastClick = time.Time{}
				if r.IsBatch {
					t.collapsed[r.Folder] = !t.collapsed[r.Folder]
				} else if t.OnAction != nil {
					t.OnAction(r, ActFolder)
				}
				continue
			}
			st.lastClick = now
			if r.IsBatch && int(e.Position.X) < gtx.Dp(36) {
				t.collapsed[r.Folder] = !t.collapsed[r.Folder]
				continue
			}
			t.clickRow(i, e.Modifiers)
		}
	}
	hovered := t.hoverKey == r.Key
	for st.check.Clicked(gtx) {
		t.toggleCheck(r)
	}
	for st.toggle.Clicked(gtx) {
		if t.OnAction != nil {
			t.OnAction(r, ActToggle)
		}
	}
	for st.folder.Clicked(gtx) {
		if t.OnAction != nil {
			t.OnAction(r, ActFolder)
		}
	}
	for st.more.Clicked(gtx) {
		if !t.selected[r.Key] {
			t.clickRow(i, 0)
		}
		t.MenuRequested = true
	}

	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, st)
	area.Pop()

	sel := t.selected[r.Key]
	checked := !r.IsBatch && t.checked[r.UID]
	switch {
	case sel || checked:
		fillRect(gtx, image.Rectangle{Max: size}, th.P.RowSelected)
	case hovered:
		fillRect(gtx, image.Rectangle{Max: size}, th.P.RowHover)
	case r.IsBatch:
		fillRect(gtx, image.Rectangle{Max: size}, th.P.SurfaceAlt)
	}
	if sel {
		fillRect(gtx, image.Rect(0, 0, gtx.Dp(3), h), th.P.Accent)
	}
	if t.cursor == r.Key && gtx.Focused(t) && !sel {
		box(gtx, image.Rectangle{Max: size}, 0, colorT{}, withAlpha(th.P.Focus, 0x60))
	}
	fillRect(gtx, image.Rect(0, h-gtx.Dp(1), w, h), th.P.Border)

	gtx.Constraints = layout.Exact(size)
	if r.IsBatch {
		th.batchContent(gtx, t, st, r, hovered)
	} else {
		th.taskContent(gtx, t, st, r, hovered)
	}
	return D{Size: size}
}

func (th *Theme) rowCheck(t *Table, st *rowState, r Row) layout.FlexChild {
	return layout.Rigid(func(gtx C) D {
		return st.check.Layout(gtx, func(gtx C) D {
			var all, some bool
			if r.IsBatch {
				all, some = t.batchCheck(r.Folder)
			} else {
				all = t.checked[r.UID]
			}
			d := th.checkSquare(gtx, all, some, st.check.Hovered())
			pointerCursor(gtx, d.Size, pointer.CursorPointer)
			return d
		})
	})
}

// extBadge is the coloured file-type tile.
func (th *Theme) extBadge(gtx C, name string) D {
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
	if len(ext) > 4 || ext == "" {
		ext = "FILE"
	}
	col := th.P.TextMuted
	switch ext {
	case "RAR", "ZIP", "7Z", "001":
		col = th.P.Purple
	case "EXE", "MSI":
		col = th.P.Info
	case "ISO", "BIN":
		col = th.P.Warning
	}
	s := gtx.Dp(32)
	r := image.Rect(0, 0, s, s)
	box(gtx, r, gtx.Dp(7), withAlpha(col, 0x22), colorT{})
	cg := gtx
	cg.Constraints = layout.Exact(r.Max)
	layout.Center.Layout(cg, func(gtx C) D {
		return th.Ellipsized(gtx, ext, unit.Sp(9), col, font.Bold, text.Middle)
	})
	return D{Size: r.Max}
}

// statusColumn shows the status pill, or the row's actions while hovered.
func (th *Theme) statusColumn(st *rowState, r Row, hovered bool) layout.FlexChild {
	return layout.Rigid(func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Dp(150)
		return layout.E.Layout(gtx, func(gtx C) D {
			if hovered {
				return th.rowActions(gtx, st, r)
			}
			return th.statusPill(gtx, r.Status)
		})
	})
}

func (th *Theme) taskContent(gtx C, t *Table, st *rowState, r Row, hovered bool) {
	layout.Inset{Left: unit.Dp(38), Right: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
		return vcenter(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				th.rowCheck(t, st, r),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx C) D { return th.extBadge(gtx, r.Name) }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return th.Ellipsized(gtx, r.Name, unit.Sp(13), th.P.Text, font.Medium, text.Start)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
						layout.Rigid(func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, func(gtx C) D {
									return th.Ellipsized(gtx, r.Meta(), unit.Sp(12), th.P.TextMuted, font.Normal, text.Start)
								}),
								layout.Rigid(func(gtx C) D {
									return th.Ellipsized(gtx, r.ProgText, unit.Sp(12), th.P.TextMuted, font.Medium, text.End)
								}),
							)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(5)}.Layout),
						layout.Rigid(func(gtx C) D { return th.thinBar(gtx, r) }),
					)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				th.statusColumn(st, r, hovered),
			)
		})
	})
}

func (th *Theme) batchContent(gtx C, t *Table, st *rowState, r Row, hovered bool) {
	open := !t.collapsed[r.Folder]
	layout.Inset{Left: unit.Dp(10), Right: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
		return vcenter(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					ic := icCollapse
					if open {
						ic = icExpand
					}
					return IconAt(gtx, ic, th.P.TextMuted, unit.Dp(18))
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				th.rowCheck(t, st, r),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx C) D {
					ic := icFolder
					if open {
						ic = icFolderOp
					}
					s := gtx.Dp(32)
					box(gtx, image.Rect(0, 0, s, s), gtx.Dp(7), withAlpha(th.P.Warning, 0x22), colorT{})
					st := op.Offset(image.Pt(s/5, s/5)).Push(gtx.Ops)
					IconAt(gtx, ic, th.P.Warning, unit.Dp(19))
					st.Pop()
					return D{Size: image.Pt(s, s)}
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, func(gtx C) D {
									return th.Ellipsized(gtx, r.Name, unit.Sp(14), th.P.Text, font.SemiBold, text.Start)
								}),
								layout.Rigid(func(gtx C) D {
									return th.Ellipsized(gtx, r.BatchMeta(), unit.Sp(12), th.P.TextMuted, font.Normal, text.End)
								}),
							)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(func(gtx C) D { return th.thinBar(gtx, r) }),
					)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				th.statusColumn(st, r, hovered),
			)
		})
	})
}

// rowActions are the inline buttons shown on hover.
func (th *Theme) rowActions(gtx C, st *rowState, r Row) D {
	toggleIc := icPlay
	switch r.Status {
	case engine.StDownloading, engine.StStarting, "Active", engine.StPending, engine.StSolving, engine.StWaitingUser:
		toggleIc = icPause
	}
	return layout.Inset{}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return th.IconButton(gtx, &st.toggle, toggleIc, th.P.TextMuted, unit.Dp(30)) }),
			layout.Rigid(func(gtx C) D { return th.IconButton(gtx, &st.folder, icFolderOp, th.P.TextMuted, unit.Dp(30)) }),
			layout.Rigid(func(gtx C) D { return th.IconButton(gtx, &st.more, icMore, th.P.TextMuted, unit.Dp(30)) }),
		)
	})
}

func (th *Theme) statusPill(gtx C, status string) D {
	if status == "" {
		return D{}
	}
	tc := tagKind(&th.P, status)
	gtx.Constraints.Min = image.Point{}
	return background(gtx, unit.Dp(10), tc.Bg, tc.Border,
		layout.Inset{Left: unit.Dp(9), Right: unit.Dp(10), Top: unit.Dp(3), Bottom: unit.Dp(3)},
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					s := gtx.Dp(6)
					r := image.Rect(0, 0, s, s)
					box(gtx, r, s/2, tc.Fg, colorT{})
					return D{Size: r.Max}
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Max.X = gtx.Dp(130)
					return th.Ellipsized(gtx, status, unit.Sp(11), tc.Fg, font.SemiBold, text.Start)
				}),
			)
		})
}

// thinBar is the 4dp progress line.
func (th *Theme) thinBar(gtx C, r Row) D {
	w, h := gtx.Constraints.Max.X, gtx.Dp(4)
	bar := image.Rect(0, 0, w, h)
	box(gtx, bar, h/2, th.P.Trough, colorT{})
	p := r.Progress
	if p < 0 {
		p = 0
	} else if p > 100 {
		p = 100
	}
	if fw := int(float64(w) * p / 100); fw > 0 {
		g := barColors(&th.P, r.Status)
		gradientBox(gtx, image.Rect(0, 0, max(fw, h), h), h/2, g[0], g[1])
	}
	return D{Size: image.Pt(w, h)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
