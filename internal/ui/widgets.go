package ui

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
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
	"gioui.org/widget/material"
)

type colorT = color.NRGBA

const pointerHand = pointer.CursorPointer

// --- drawing helpers ---------------------------------------------------------

func pt(x, y float32) f32.Point { return f32.Pt(x, y) }

func paintStroke(gtx C, p clip.PathSpec, c color.NRGBA, w float32) {
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: p, Width: w}.Op())
}

func fillRect(gtx C, r image.Rectangle, c color.NRGBA) {
	paint.FillShape(gtx.Ops, c, clip.Rect(r).Op())
}

// box draws a rounded rectangle with an optional 1dp border.
func box(gtx C, r image.Rectangle, radius int, bg, border color.NRGBA) {
	rr := clip.RRect{Rect: r, NE: radius, NW: radius, SE: radius, SW: radius}
	if bg.A > 0 {
		paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
	}
	if border.A > 0 {
		paint.FillShape(gtx.Ops, border, clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(gtx.Dp(1))}.Op())
	}
}

func gradientBox(gtx C, r image.Rectangle, radius int, from, to color.NRGBA) {
	rr := clip.RRect{Rect: r, NE: radius, NW: radius, SE: radius, SW: radius}
	defer rr.Push(gtx.Ops).Pop()
	paint.LinearGradientOp{
		Stop1: f32.Pt(float32(r.Min.X), float32(r.Min.Y)), Color1: from,
		Stop2: f32.Pt(float32(r.Max.X), float32(r.Max.Y)), Color2: to,
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// background lays w out over a rounded box.
func background(gtx C, radius unit.Dp, bg, border color.NRGBA, inset layout.Inset, w layout.Widget) D {
	m := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, w)
	call := m.Stop()
	box(gtx, image.Rectangle{Max: dims.Size}, gtx.Dp(radius), bg, border)
	call.Add(gtx.Ops)
	return dims
}

func pointerCursor(gtx C, size image.Point, c pointer.Cursor) {
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	c.Add(gtx.Ops)
}

// --- hover tracking ------------------------------------------------------------

// Hover reports whether the pointer is over the last laid-out area.
type Hover struct {
	hovered bool
	Pos     f32.Point
}

// Update consumes pointer events; call before Add each frame.
func (h *Hover) Update(gtx C) bool {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: h, Kinds: pointer.Enter | pointer.Leave | pointer.Move | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Enter, pointer.Move:
			h.hovered, h.Pos = true, e.Position
		case pointer.Leave, pointer.Cancel:
			h.hovered = false
		}
	}
	return h.hovered
}

// Add registers the area of size for hover tracking.
func (h *Hover) Add(gtx C, size image.Point) {
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, h)
}

// Hovered is the state from the last Update.
func (h *Hover) Hovered() bool { return h.hovered }

// --- buttons --------------------------------------------------------------------

// Button draws a themed text button.
func (t *Theme) Button(gtx C, b *widget.Clickable, label string, colors ButtonColors) D {
	return t.IconLabelButton(gtx, b, nil, label, colors)
}

func (t *Theme) buttonSized(gtx C, b *widget.Clickable, label string, colors ButtonColors, small bool) D {
	return t.button(gtx, b, nil, label, colors, small)
}

// IconLabelButton draws a button with an optional leading icon.
func (t *Theme) IconLabelButton(gtx C, b *widget.Clickable, ic *widget.Icon, label string, colors ButtonColors) D {
	return t.button(gtx, b, ic, label, colors, false)
}

func (t *Theme) button(gtx C, b *widget.Clickable, ic *widget.Icon, label string, colors ButtonColors, small bool) D {
	pad := layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(12), Right: unit.Dp(12)}
	if small {
		pad = layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(9), Right: unit.Dp(9)}
	}
	if ic != nil {
		pad.Left = pad.Left - 2
	}
	return b.Layout(gtx, func(gtx C) D {
		bg, border := colors.Bg, colors.Border
		if b.Hovered() {
			bg, border = colors.HoverBg, colors.HoverBorder
		}
		if b.Pressed() && bg.A > 0 {
			bg = mix(bg, color.NRGBA{A: 255}, 0.12)
		}
		if gtx.Focused(b) {
			border = t.P.Focus
		}
		m := op.Record(gtx.Ops)
		dims := pad.Layout(gtx, func(gtx C) D {
			children := []layout.FlexChild{}
			if ic != nil {
				children = append(children, layout.Rigid(func(gtx C) D {
					sz := gtx.Dp(16)
					gtx.Constraints = layout.Exact(image.Pt(sz, sz))
					return ic.Layout(gtx, colors.Fg)
				}))
				if label != "" {
					children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
				}
			}
			if label != "" {
				children = append(children, layout.Rigid(func(gtx C) D {
					l := t.Label(unit.Sp(13), label, colors.Fg)
					l.Font.Weight = font.Medium
					l.MaxLines = 1
					return l.Layout(gtx)
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
		call := m.Stop()
		box(gtx, image.Rectangle{Max: dims.Size}, gtx.Dp(6), bg, border)
		call.Add(gtx.Ops)
		pointerCursor(gtx, dims.Size, pointer.CursorPointer)
		return dims
	})
}

// IconButton is a square ghost button holding one icon.
func (t *Theme) IconButton(gtx C, b *widget.Clickable, ic *widget.Icon, col color.NRGBA, size unit.Dp) D {
	return b.Layout(gtx, func(gtx C) D {
		s := gtx.Dp(size)
		r := image.Rect(0, 0, s, s)
		if b.Hovered() {
			box(gtx, r, gtx.Dp(6), t.P.Raised, colorT{})
			col = t.P.Text
		}
		in := s / 5
		st := op.Offset(image.Pt(in, in)).Push(gtx.Ops)
		cg := gtx
		cg.Constraints = layout.Exact(image.Pt(s-2*in, s-2*in))
		ic.Layout(cg, col)
		st.Pop()
		pointerCursor(gtx, r.Max, pointer.CursorPointer)
		return D{Size: r.Max}
	})
}

// IconAt draws ic at size dp.
func IconAt(gtx C, ic *widget.Icon, col color.NRGBA, size unit.Dp) D {
	s := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(s, s))
	return ic.Layout(gtx, col)
}

func mix(a, b color.NRGBA, f float32) color.NRGBA {
	m := func(x, y uint8) uint8 { return uint8(float32(x)*(1-f) + float32(y)*f) }
	return color.NRGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: a.A}
}

// LinkButton is borderless clickable text.
func (t *Theme) LinkButton(gtx C, b *widget.Clickable, label string) D {
	return b.Layout(gtx, func(gtx C) D {
		col := t.P.Link
		if b.Hovered() {
			col = t.P.Text
		}
		d := t.Label(unit.Sp(13), label, col).Layout(gtx)
		pointerCursor(gtx, d.Size, pointer.CursorPointer)
		return d
	})
}

// --- check box -------------------------------------------------------------------

// CheckBox draws a square check box with an optional label. tri draws the
// "some checked" dash.
func (t *Theme) CheckBox(gtx C, b *widget.Bool, label string) D {
	return b.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return t.checkSquare(gtx, b.Value, false, b.Hovered()) }),
			layout.Rigid(func(gtx C) D {
				if label == "" {
					return D{}
				}
				return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, t.Label(unit.Sp(13), label, t.P.Text).Layout)
			}),
		)
	})
}

func (t *Theme) checkSquare(gtx C, checked, partial, hovered bool) D {
	sz := gtx.Dp(15)
	r := image.Rect(0, 0, sz, sz)
	border := t.P.BorderStrong
	if hovered {
		border = t.P.Focus
	}
	if checked || partial {
		box(gtx, r, gtx.Dp(4), t.P.Selection, t.P.Selection)
		var p clip.Path
		p.Begin(gtx.Ops)
		s := float32(sz)
		if checked {
			p.MoveTo(f32.Pt(s*0.24, s*0.52))
			p.LineTo(f32.Pt(s*0.43, s*0.70))
			p.LineTo(f32.Pt(s*0.77, s*0.31))
		} else {
			p.MoveTo(f32.Pt(s*0.25, s*0.5))
			p.LineTo(f32.Pt(s*0.75, s*0.5))
		}
		paint.FillShape(gtx.Ops, t.P.SelectionText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
	} else {
		box(gtx, r, gtx.Dp(4), t.P.Input, border)
	}
	return D{Size: r.Max}
}

// Radio draws a radio button bound to e for key.
func (t *Theme) Radio(gtx C, e *widget.Enum, key, label string) D {
	return e.Layout(gtx, key, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(15)
				r := image.Rect(0, 0, sz, sz)
				on := e.Value == key
				border := t.P.BorderStrong
				if h, ok := e.Hovered(); ok && h == key {
					border = t.P.Focus
				}
				if on {
					border = t.P.Selection
				}
				paint.FillShape(gtx.Ops, t.P.Input, clip.Ellipse(r).Op(gtx.Ops))
				paint.FillShape(gtx.Ops, border, clip.Stroke{Path: clip.Ellipse(r).Path(gtx.Ops), Width: float32(gtx.Dp(1.5))}.Op())
				if on {
					in := gtx.Dp(4)
					paint.FillShape(gtx.Ops, t.P.Selection, clip.Ellipse(r.Inset(in)).Op(gtx.Ops))
				}
				return D{Size: r.Max}
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, t.Label(unit.Sp(13), label, t.P.Text).Layout)
			}),
		)
	})
}

// --- text fields -----------------------------------------------------------------

// Field draws an editor in a bordered box.
func (t *Theme) Field(gtx C, ed *widget.Editor, hint string, minHeight unit.Dp) D {
	border := t.P.Border
	if gtx.Focused(ed) {
		border = t.P.Focus
	}
	return background(gtx, unit.Dp(6), t.P.Input, border,
		layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)},
		func(gtx C) D {
			if minHeight > 0 {
				gtx.Constraints.Min.Y = gtx.Dp(minHeight)
				gtx.Constraints.Max.Y = gtx.Constraints.Min.Y
			}
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			e := material.Editor(t.Theme, ed, hint)
			e.Color = t.P.Text
			e.HintColor = t.P.TextSubtle
			e.SelectionColor = withAlpha(t.P.Selection, 0x80)
			e.TextSize = unit.Sp(13)
			return e.Layout(gtx)
		})
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// Stepper is a bounded integer input with − and + buttons.
type Stepper struct {
	Min, Max int
	// Width of the number field (dp); 0 means 64.
	Width    unit.Dp
	ed       widget.Editor
	dec, inc widget.Clickable
	val      int
	init     bool
}

// Set sets the value.
func (s *Stepper) Set(v int) {
	s.val = clampInt(v, s.Min, s.Max)
	s.ed.SingleLine = true
	s.ed.Filter = "0123456789"
	s.ed.SetText(strconv.Itoa(s.val))
	s.init = true
}

// Value returns the current value.
func (s *Stepper) Value() int {
	if v, err := strconv.Atoi(strings.TrimSpace(s.ed.Text())); err == nil {
		return clampInt(v, s.Min, s.Max)
	}
	return s.val
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Stepper draws s; zeroLabel replaces 0 (e.g. "Unlimited"), suffix follows
// the number.
func (t *Theme) Stepper(gtx C, s *Stepper, suffix, zeroLabel string) D {
	if !s.init {
		s.Set(s.Min)
	}
	for s.dec.Clicked(gtx) {
		s.Set(s.Value() - 1)
	}
	for s.inc.Clicked(gtx) {
		s.Set(s.Value() + 1)
	}
	for {
		ev, ok := s.ed.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			s.Set(s.Value())
		}
	}
	if !gtx.Focused(&s.ed) && s.ed.Text() != strconv.Itoa(s.Value()) {
		s.Set(s.Value())
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return t.buttonSized(gtx, &s.dec, "−", t.P.Button, true) }),
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
				w := s.Width
				if w == 0 {
					w = 64
				}
				gtx.Constraints.Max.X = gtx.Dp(w)
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return t.Field(gtx, &s.ed, "", 0)
			})
		}),
		layout.Rigid(func(gtx C) D { return t.buttonSized(gtx, &s.inc, "+", t.P.Button, true) }),
		layout.Rigid(func(gtx C) D {
			txt := suffix
			if s.Value() == 0 && zeroLabel != "" {
				txt = zeroLabel
			}
			if txt == "" {
				return D{}
			}
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, t.Label(unit.Sp(12), txt, t.P.TextMuted).Layout)
		}),
	)
}

// Segmented is a single-choice row of options (theme, AM/PM, ...).
type Segmented struct {
	Value string
	btns  map[string]*widget.Clickable
}

// Segmented draws options; the selected one is highlighted.
func (t *Theme) Segmented(gtx C, s *Segmented, options ...string) D {
	if s.btns == nil {
		s.btns = map[string]*widget.Clickable{}
	}
	children := make([]layout.FlexChild, 0, len(options))
	for _, o := range options {
		o := o
		b := s.btns[o]
		if b == nil {
			b = new(widget.Clickable)
			s.btns[o] = b
		}
		for b.Clicked(gtx) {
			s.Value = o
		}
		children = append(children, layout.Rigid(func(gtx C) D {
			col := t.P.Button
			if s.Value == o {
				col = t.P.Primary
			}
			return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
				return t.buttonSized(gtx, b, o, col, true)
			})
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// --- text helpers -----------------------------------------------------------------

// SectionTitle is the small upper-case card heading.
func (t *Theme) SectionTitle(gtx C, txt string) D {
	l := t.Label(unit.Sp(11), strings.ToUpper(txt), t.P.TextSubtle)
	l.Font.Weight = font.SemiBold
	return l.Layout(gtx)
}

// Body draws wrapped body text.
func (t *Theme) Body(gtx C, txt string) D {
	return t.Label(unit.Sp(13), txt, t.P.Text).Layout(gtx)
}

// Ellipsized draws one line, cut with "…".
func (t *Theme) Ellipsized(gtx C, txt string, size unit.Sp, col color.NRGBA, weight font.Weight, align text.Alignment) D {
	l := t.Label(size, txt, col)
	l.MaxLines = 1
	l.LineHeightScale = 1
	l.Truncator = "…"
	l.Font.Weight = weight
	l.Alignment = align
	return l.Layout(gtx)
}

// Divider draws a 1dp horizontal rule.
func (t *Theme) Divider(gtx C) D {
	h := gtx.Dp(1)
	fillRect(gtx, image.Rect(0, 0, gtx.Constraints.Max.X, h), t.P.Border)
	return D{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

// --- tooltip ----------------------------------------------------------------------

// Tooltip draws txt in a floating box near pos, kept inside win (both in
// window coordinates). Call it last in the frame so it paints on top.
func (t *Theme) Tooltip(gtx C, pos, win image.Point, txt string) {
	if txt == "" {
		return
	}
	gtx.Constraints.Min = image.Point{}
	gtx.Constraints.Max = image.Pt(gtx.Dp(380), win.Y)
	m := op.Record(gtx.Ops)
	dims := background(gtx, unit.Dp(6), t.P.TooltipBg, colorT{},
		layout.UniformInset(unit.Dp(7)), func(gtx C) D {
			return t.Label(unit.Sp(12), txt, t.P.TooltipText).Layout(gtx)
		})
	call := m.Stop()
	x, y := pos.X+gtx.Dp(12), pos.Y+gtx.Dp(18)
	if x+dims.Size.X > win.X {
		x = win.X - dims.Size.X - gtx.Dp(4)
	}
	if y+dims.Size.Y > win.Y {
		y = pos.Y - dims.Size.Y - gtx.Dp(6)
	}
	defer op.Offset(image.Pt(max(0, x), max(0, y))).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
}

// keyPressed drains key presses matching filters and reports the names.
func keyPressed(gtx C, filters ...event.Filter) []key.Event {
	var out []key.Event
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return out
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			out = append(out, e)
		}
	}
}
