package ui

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// SpeedGraph is the live global-speed sparkline (35 samples).
type SpeedGraph struct {
	history []float64
}

// NewSpeedGraph keeps n samples.
func NewSpeedGraph(n int) *SpeedGraph { return &SpeedGraph{history: make([]float64, n)} }

// Add appends a sample in MB/s.
func (g *SpeedGraph) Add(v float64) {
	copy(g.history, g.history[1:])
	g.history[len(g.history)-1] = v
}

// SpeedGraph draws the sparkline filling width × height.
func (t *Theme) SpeedGraph(gtx C, g *SpeedGraph, height unit.Dp) D {
	w, h := gtx.Constraints.Max.X, gtx.Dp(height)
	r := image.Rect(0, 0, w, h)
	n := len(g.history)
	if n < 2 || w <= 2 {
		return D{Size: r.Max}
	}
	maxV := 0.0
	for _, v := range g.history {
		maxV = math.Max(maxV, v)
	}
	if maxV <= 0.05 {
		maxV = 1
	} else {
		maxV *= 1.25
	}
	fw, fh := float32(w), float32(h)
	pad := float32(gtx.Dp(2))
	drawH := fh - 2*pad
	step := (fw - 2) / float32(n-1)
	pts := make([]f32.Point, n)
	for i, v := range g.history {
		ratio := float32(math.Min(1, math.Max(0, v/maxV)))
		pts[i] = f32.Pt(1+float32(i)*step, fh-pad-ratio*drawH)
	}

	var fill clip.Path
	fill.Begin(gtx.Ops)
	fill.MoveTo(f32.Pt(pts[0].X, fh-1))
	for _, p := range pts {
		fill.LineTo(p)
	}
	fill.LineTo(f32.Pt(pts[n-1].X, fh-1))
	fill.Close()
	st := clip.Outline{Path: fill.End()}.Op().Push(gtx.Ops)
	paint.LinearGradientOp{Stop1: f32.Pt(0, 0), Color1: t.P.GraphFillTop, Stop2: f32.Pt(0, fh), Color2: t.P.GraphFillBot}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	st.Pop()

	var line clip.Path
	line.Begin(gtx.Ops)
	line.MoveTo(pts[0])
	for _, p := range pts[1:] {
		line.LineTo(p)
	}
	paint.FillShape(gtx.Ops, t.P.GraphLine, clip.Stroke{Path: line.End(), Width: float32(gtx.Dp(1.5))}.Op())
	return D{Size: r.Max}
}

// ScheduleClock is the 20dp clock shown when a schedule is armed. Its hands
// point at the window start; green while the window is open.
type ScheduleClock struct {
	Click widget.Clickable
	Hover Hover
}

// ScheduleClock draws the icon for start ("HH:mm").
func (t *Theme) ScheduleClock(gtx C, c *ScheduleClock, start string, active bool) D {
	return c.Click.Layout(gtx, func(gtx C) D {
		sz := gtx.Dp(20)
		s := float32(sz)
		cx, cy := s/2, s/2
		halo := withAlpha(t.P.ClockBlue, 30)
		bezel := t.P.ClockBlue
		if active {
			halo, bezel = withAlpha(t.P.ClockGreen, 35), t.P.ClockGreen
		}
		circle := func(r float32) clip.Ellipse {
			return clip.Ellipse{Min: image.Pt(int(cx-r), int(cy-r)), Max: image.Pt(int(cx+r+0.5), int(cy+r+0.5))}
		}
		paint.FillShape(gtx.Ops, halo, circle(s*0.475).Op(gtx.Ops))
		r := s * 0.36
		paint.FillShape(gtx.Ops, t.P.ClockFace, circle(r).Op(gtx.Ops))
		paint.FillShape(gtx.Ops, bezel, clip.Stroke{Path: circle(r).Path(gtx.Ops), Width: s * 0.08}.Op())

		stroke := func(a, b f32.Point, col colorT, w float32) {
			var p clip.Path
			p.Begin(gtx.Ops)
			p.MoveTo(a)
			p.LineTo(b)
			paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: w}.Op())
		}
		for _, d := range [][2]float32{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
			stroke(f32.Pt(cx+d[0]*(r-s*0.06), cy+d[1]*(r-s*0.06)), f32.Pt(cx+d[0]*(r-s*0.15), cy+d[1]*(r-s*0.15)), t.P.ClockTick, s*0.05)
		}
		hh, mm := 10, 10
		if len(start) == 5 {
			if h, m, ok := parseHM(start); ok {
				hh, mm = h, m
			}
		}
		polar := func(deg, l float32) f32.Point {
			a := float64(deg) * math.Pi / 180
			return f32.Pt(cx+float32(math.Sin(a))*l, cy-float32(math.Cos(a))*l)
		}
		hand := t.P.ClockHand
		if active {
			hand = t.P.ClockGreen
		}
		stroke(f32.Pt(cx, cy), polar(float32(hh%12)*30+float32(mm)*0.5, s*0.19), hand, s*0.08)
		stroke(f32.Pt(cx, cy), polar(float32(mm)*6, s*0.26), t.P.ClockHand, s*0.06)
		paint.FillShape(gtx.Ops, bezel, circle(s*0.065).Op(gtx.Ops))

		c.Hover.Update(gtx)
		c.Hover.Add(gtx, image.Pt(sz, sz))
		pointerCursor(gtx, image.Pt(sz, sz), pointerHand)
		return D{Size: image.Pt(sz, sz)}
	})
}

func parseHM(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
