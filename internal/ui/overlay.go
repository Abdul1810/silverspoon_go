package ui

import (
	"image"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// ButtonKind picks a dialog button's colours.
type ButtonKind int

const (
	KindNormal ButtonKind = iota
	KindPrimary
	KindDanger
)

// DialogButton is one button in a dialog footer. OnClick returns false to
// keep the dialog open (e.g. validation failed).
type DialogButton struct {
	Label   string
	Kind    ButtonKind
	OnClick func() bool
	click   widget.Clickable
}

// Dialog is a modal card over a scrim. Escape triggers the Cancel button
// (the last one, unless CancelIndex says otherwise); Enter the default.
type Dialog struct {
	Title   string
	Width   unit.Dp
	Body    func(gtx C) D
	Buttons []*DialogButton
	// Default is the index of the button Enter triggers, -1 for none.
	Default int
	// Cancel is the index Escape triggers, -1 to close without action.
	Cancel int

	closed bool
	list   widget.List
}

// Close dismisses the dialog.
func (d *Dialog) Close() { d.closed = true }

// Btn makes a dialog button.
func Btn(label string, kind ButtonKind, on func() bool) *DialogButton {
	return &DialogButton{Label: label, Kind: kind, OnClick: on}
}

func (th *Theme) kindColors(k ButtonKind) ButtonColors {
	switch k {
	case KindPrimary:
		return th.P.Primary
	case KindDanger:
		return th.P.Danger2
	}
	return th.P.Button
}

func (d *Dialog) press(i int) {
	if i < 0 || i >= len(d.Buttons) {
		d.closed = true
		return
	}
	b := d.Buttons[i]
	if b.OnClick == nil || b.OnClick() {
		d.closed = true
	}
}

// layoutDialog draws d centred over a scrim filling gtx.
func (th *Theme) layoutDialog(gtx C, d *Dialog) {
	size := gtx.Constraints.Max
	// Scrim swallows pointer input behind the dialog.
	fillRect(gtx, image.Rectangle{Max: size}, th.P.Scrim)
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, d)
	area.Pop()
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: d, Kinds: pointer.Press | pointer.Scroll}); !ok {
			break
		}
	}
	for _, e := range keyPressed(gtx, key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameReturn}, key.Filter{Name: key.NameEnter}) {
		if e.Name == key.NameEscape {
			d.press(d.Cancel)
		} else if d.Default >= 0 {
			d.press(d.Default)
		}
	}
	for i, b := range d.Buttons {
		for b.click.Clicked(gtx) {
			d.press(i)
		}
	}

	w := d.Width
	if w == 0 {
		w = 460
	}
	cw := min(gtx.Dp(w), size.X-gtx.Dp(32))
	gtx.Constraints = layout.Constraints{Min: image.Pt(cw, 0), Max: image.Pt(cw, size.Y-gtx.Dp(48))}
	m := op.Record(gtx.Ops)
	dims := th.dialogCard(gtx, d)
	call := m.Stop()
	off := image.Pt((size.X-dims.Size.X)/2, max(gtx.Dp(24), (size.Y-dims.Size.Y)/2))
	defer op.Offset(off).Push(gtx.Ops).Pop()
	// Shadow.
	sh := image.Rectangle{Max: dims.Size}.Add(image.Pt(0, gtx.Dp(4))).Inset(-gtx.Dp(2))
	paint.FillShape(gtx.Ops, withAlpha(rgb(0), 0x50), clip.UniformRRect(sh, gtx.Dp(14)).Op(gtx.Ops))
	call.Add(gtx.Ops)
}

func (th *Theme) dialogCard(gtx C, d *Dialog) D {
	d.list.Axis = layout.Vertical
	return background(gtx, unit.Dp(12), th.P.Overlay, th.P.Border, layout.Inset{}, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: unit.Dp(18), Left: unit.Dp(22), Right: unit.Dp(22), Bottom: unit.Dp(14)}.Layout(gtx, func(gtx C) D {
					l := th.Label(unit.Sp(17), d.Title, th.P.Text)
					l.Font.Weight = font.SemiBold
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(th.Divider),
			layout.Flexed(1, func(gtx C) D {
				gtx.Constraints.Min.Y = 0
				return layout.Inset{Top: unit.Dp(16), Bottom: unit.Dp(16), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx C) D {
					return d.list.List.Layout(gtx, 1, func(gtx C, _ int) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return d.Body(gtx)
					})
				})
			}),
			layout.Rigid(th.Divider),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(14), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx C) D {
					return layout.E.Layout(gtx, func(gtx C) D {
						children := make([]layout.FlexChild, 0, len(d.Buttons))
						for _, b := range d.Buttons {
							b := b
							children = append(children, layout.Rigid(func(gtx C) D {
								return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
									return th.Button(gtx, &b.click, b.Label, th.kindColors(b.Kind))
								})
							}))
						}
						return layout.Flex{}.Layout(gtx, children...)
					})
				})
			}),
		)
	})
}

// MenuItem is a menu entry; Label "" is a separator.
type MenuItem struct {
	Label    string
	Hint     string
	Checked  bool
	Disabled bool
	Action   func()
	click    widget.Clickable
}

// Sep is a menu separator.
func Sep() *MenuItem { return &MenuItem{} }

// Item makes a menu entry.
func Item(label string, action func()) *MenuItem { return &MenuItem{Label: label, Action: action} }

// Menu is a popup list of items at a window position.
type Menu struct {
	Items []*MenuItem
	At    image.Point
	open  bool
}

// layoutMenu draws an open menu; a press outside closes it.
func (th *Theme) layoutMenu(gtx C, m *Menu) {
	size := gtx.Constraints.Max
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, m)
	area.Pop()
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: m, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			m.open = false
		}
	}
	for range keyPressed(gtx, key.Filter{Name: key.NameEscape}) {
		m.open = false
	}
	for _, it := range m.Items {
		for it.click.Clicked(gtx) {
			m.open = false
			if it.Action != nil && !it.Disabled {
				it.Action()
			}
		}
	}
	if !m.open {
		return
	}
	gtx.Constraints = layout.Constraints{Max: image.Pt(min(size.X, gtx.Dp(420)), size.Y)}
	rec := op.Record(gtx.Ops)
	dims := background(gtx, unit.Dp(8), th.P.Overlay, th.P.BorderStrong, layout.UniformInset(unit.Dp(5)), func(gtx C) D {
		// Size every item to the widest one.
		widest := 0
		for _, it := range m.Items {
			if it.Label == "" {
				continue
			}
			mr := op.Record(gtx.Ops)
			d := th.menuRow(gtx, it, 0)
			mr.Stop()
			widest = max(widest, d.Size.X)
		}
		children := make([]layout.FlexChild, 0, len(m.Items))
		for _, it := range m.Items {
			it := it
			children = append(children, layout.Rigid(func(gtx C) D {
				if it.Label == "" {
					return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Max.X = widest
						return th.Divider(gtx)
					})
				}
				return th.menuRow(gtx, it, widest)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
	call := rec.Stop()
	at := m.At
	if at.X+dims.Size.X > size.X {
		at.X = size.X - dims.Size.X - gtx.Dp(2)
	}
	if at.Y+dims.Size.Y > size.Y {
		at.Y = max(0, at.Y-dims.Size.Y)
	}
	defer op.Offset(image.Pt(max(0, at.X), max(0, at.Y))).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
}

func (th *Theme) menuRow(gtx C, it *MenuItem, width int) D {
	return it.click.Layout(gtx, func(gtx C) D {
		if width > 0 {
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
		} else {
			gtx.Constraints.Min.X = 0
		}
		m := op.Record(gtx.Ops)
		dims := layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
			col := th.P.Text
			if it.Disabled {
				col = th.P.TextSubtle
			}
			check := layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(16)
				if it.Checked {
					var p clip.Path
					p.Begin(gtx.Ops)
					s := float32(sz)
					p.MoveTo(pt(s*0.2, s*0.52))
					p.LineTo(pt(s*0.42, s*0.72))
					p.LineTo(pt(s*0.8, s*0.3))
					paintStroke(gtx, p.End(), th.P.Accent, float32(gtx.Dp(1.8)))
				}
				return D{Size: image.Pt(sz, sz)}
			})
			label := layout.Rigid(th.Label(unit.Sp(13), it.Label, col).Layout)
			hint := layout.Rigid(func(gtx C) D {
				if it.Hint == "" {
					return D{}
				}
				return layout.Inset{Left: unit.Dp(28)}.Layout(gtx, th.Label(unit.Sp(12), it.Hint, th.P.TextSubtle).Layout)
			})
			gap := layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout)
			if width == 0 {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, check, gap, label, hint)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, check, gap, label,
				layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }), hint)
		})
		call := m.Stop()
		if it.click.Hovered() && !it.Disabled {
			box(gtx, image.Rectangle{Max: dims.Size}, gtx.Dp(5), th.P.AccentSoft, colorT{})
		}
		call.Add(gtx.Ops)
		return dims
	})
}
