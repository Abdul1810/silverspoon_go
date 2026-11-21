// Package ui is SilverSpoon's Gio front end: a sidebar of views and batches,
// a toolbar with search and the add button, the download list, a contextual
// selection bar, a status bar and modal sheets. Every colour comes from a
// Palette of design tokens, so Dark and Light stay consistent.
package ui

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

func rgba(v uint32, a uint8) color.NRGBA {
	c := rgb(v)
	c.A = a
	return c
}

// ButtonColors are a button variant's fill, border and text, plus hover.
type ButtonColors struct {
	Bg, Border, Fg, HoverBg, HoverBorder color.NRGBA
}

// TagColors style a status pill.
type TagColors struct{ Bg, Border, Fg color.NRGBA }

// Palette holds the design tokens.
type Palette struct {
	Dark bool

	// Surfaces, back to front.
	Window, Sidebar, Surface, SurfaceAlt, Raised, Input, Overlay color.NRGBA
	Border, BorderStrong, Focus                                  color.NRGBA
	Text, TextMuted, TextSubtle                                  color.NRGBA

	// One accent; semantic colours carry meaning only.
	Accent, AccentSoft, AccentText color.NRGBA
	Success, Warning, Danger, Info color.NRGBA
	Purple, Link                   color.NRGBA

	Selection, SelectionText, RowHover, RowSelected color.NRGBA
	TooltipBg, TooltipText, Scrim                   color.NRGBA

	Button, Primary, Danger2, Ghost ButtonColors

	TagError, TagDone, TagActive, TagPaused, TagOther TagColors

	Trough                                                 color.NRGBA
	BarActive, BarDone, BarError, BarExtract               [2]color.NRGBA
	GraphLine, GraphFillTop, GraphFillBot                  color.NRGBA
	ClockBlue, ClockGreen, ClockFace, ClockHand, ClockTick color.NRGBA
}

// DarkPalette is the default theme.
func DarkPalette() Palette {
	accent := rgb(0x7c8cff)
	return Palette{
		Dark:   true,
		Window: rgb(0x0e1014), Sidebar: rgb(0x0b0d10), Surface: rgb(0x13161b), SurfaceAlt: rgb(0x171a20),
		Raised: rgb(0x1c2027), Input: rgb(0x0f1115), Overlay: rgb(0x181b21),
		Border: rgb(0x23272f), BorderStrong: rgb(0x323844), Focus: accent,
		Text: rgb(0xe8eaef), TextMuted: rgb(0x9aa2b1), TextSubtle: rgb(0x6b7282),

		Accent: accent, AccentSoft: rgba(0x7c8cff, 0x26), AccentText: rgb(0xaab6ff),
		Success: rgb(0x3ecf8e), Warning: rgb(0xf5b544), Danger: rgb(0xf2555a), Info: rgb(0x4cc2ff),
		Purple: rgb(0xa78bfa), Link: rgb(0x8ea0ff),

		Selection: accent, SelectionText: rgb(0xffffff),
		RowHover: rgb(0x181c22), RowSelected: rgba(0x7c8cff, 0x24),
		TooltipBg: rgb(0x262b34), TooltipText: rgb(0xf1f3f7), Scrim: rgba(0x000000, 0xa0),

		Button:  ButtonColors{rgb(0x1c2027), rgb(0x2b303a), rgb(0xd9dde6), rgb(0x232832), rgb(0x3a414d)},
		Primary: ButtonColors{rgb(0x5b6cf5), rgb(0x6b7bff), rgb(0xffffff), rgb(0x6b7bff), rgb(0x8592ff)},
		Danger2: ButtonColors{rgba(0xf2555a, 0x22), rgba(0xf2555a, 0x66), rgb(0xff8a8e), rgba(0xf2555a, 0x38), rgba(0xf2555a, 0x99)},
		Ghost:   ButtonColors{color.NRGBA{}, color.NRGBA{}, rgb(0xc5cad5), rgb(0x1f232a), color.NRGBA{}},

		TagError:  TagColors{rgba(0xf2555a, 0x1f), rgba(0xf2555a, 0x00), rgb(0xff8a8e)},
		TagDone:   TagColors{rgba(0x3ecf8e, 0x1c), rgba(0x3ecf8e, 0x00), rgb(0x6ee7b0)},
		TagActive: TagColors{rgba(0x4cc2ff, 0x1f), rgba(0x4cc2ff, 0x00), rgb(0x8fd8ff)},
		TagPaused: TagColors{rgba(0xf5b544, 0x1c), rgba(0xf5b544, 0x00), rgb(0xf8cd7a)},
		TagOther:  TagColors{rgba(0x9aa2b1, 0x18), rgba(0x9aa2b1, 0x00), rgb(0xb8beca)},

		Trough:    rgb(0x22262e),
		BarActive: [2]color.NRGBA{rgb(0x4cc2ff), rgb(0x7c8cff)}, BarDone: [2]color.NRGBA{rgb(0x2fbf7f), rgb(0x3ecf8e)},
		BarError: [2]color.NRGBA{rgb(0x5b6273), rgb(0x6b7282)}, BarExtract: [2]color.NRGBA{rgb(0x8b6cf6), rgb(0xa78bfa)},

		GraphLine: rgb(0x3ecf8e), GraphFillTop: rgba(0x3ecf8e, 0x55), GraphFillBot: rgba(0x3ecf8e, 0x00),
		ClockBlue: rgb(0x7c8cff), ClockGreen: rgb(0x3ecf8e), ClockFace: rgb(0x0e1014),
		ClockHand: rgb(0xe8eaef), ClockTick: rgba(0x9aa2b1, 200),
	}
}

// LightPalette is the light theme.
func LightPalette() Palette {
	accent := rgb(0x4255f0)
	return Palette{
		Window: rgb(0xf6f7f9), Sidebar: rgb(0xeef0f4), Surface: rgb(0xffffff), SurfaceAlt: rgb(0xf8f9fb),
		Raised: rgb(0xf1f3f6), Input: rgb(0xffffff), Overlay: rgb(0xffffff),
		Border: rgb(0xe2e5ea), BorderStrong: rgb(0xc7ccd5), Focus: accent,
		Text: rgb(0x14171c), TextMuted: rgb(0x586170), TextSubtle: rgb(0x8a92a0),

		Accent: accent, AccentSoft: rgba(0x4255f0, 0x1a), AccentText: rgb(0x3344d6),
		Success: rgb(0x16a36a), Warning: rgb(0xc98a12), Danger: rgb(0xdc3b41), Info: rgb(0x0a8fd6),
		Purple: rgb(0x7c4ddb), Link: accent,

		Selection: accent, SelectionText: rgb(0xffffff),
		RowHover: rgb(0xf5f6f9), RowSelected: rgba(0x4255f0, 0x14),
		TooltipBg: rgb(0x1d2129), TooltipText: rgb(0xffffff), Scrim: rgba(0x14171c, 0x66),

		Button:  ButtonColors{rgb(0xffffff), rgb(0xd5d9e0), rgb(0x1f242c), rgb(0xf3f4f7), rgb(0xb9bfca)},
		Primary: ButtonColors{accent, rgb(0x3445d9), rgb(0xffffff), rgb(0x3445d9), rgb(0x2a39c0)},
		Danger2: ButtonColors{rgb(0xfdecec), rgb(0xf5b5b7), rgb(0xc4262c), rgb(0xfbdcdd), rgb(0xee9497)},
		Ghost:   ButtonColors{color.NRGBA{}, color.NRGBA{}, rgb(0x3b4250), rgb(0xe9ecf1), color.NRGBA{}},

		TagError:  TagColors{rgb(0xfdecec), color.NRGBA{}, rgb(0xb4232a)},
		TagDone:   TagColors{rgb(0xe6f6ee), color.NRGBA{}, rgb(0x0f7a4e)},
		TagActive: TagColors{rgb(0xe5f3fc), color.NRGBA{}, rgb(0x0a6aa3)},
		TagPaused: TagColors{rgb(0xfdf3df), color.NRGBA{}, rgb(0x8c5d06)},
		TagOther:  TagColors{rgb(0xeef0f3), color.NRGBA{}, rgb(0x4a5260)},

		Trough:    rgb(0xe8ebf0),
		BarActive: [2]color.NRGBA{rgb(0x0a8fd6), rgb(0x4255f0)}, BarDone: [2]color.NRGBA{rgb(0x139058), rgb(0x16a36a)},
		BarError: [2]color.NRGBA{rgb(0x9aa2b1), rgb(0x8a92a0)}, BarExtract: [2]color.NRGBA{rgb(0x6a3fd0), rgb(0x8b62e8)},

		GraphLine: rgb(0x16a36a), GraphFillTop: rgba(0x16a36a, 0x40), GraphFillBot: rgba(0x16a36a, 0x00),
		ClockBlue: accent, ClockGreen: rgb(0x16a36a), ClockFace: rgb(0xffffff),
		ClockHand: rgb(0x14171c), ClockTick: rgba(0x586170, 190),
	}
}

// Theme bundles the material theme (fonts, shaper) with the palette.
type Theme struct {
	*material.Theme
	P Palette
}

// NewTheme builds a theme for "Dark" or "Light".
func NewTheme(name string) *Theme {
	mt := material.NewTheme()
	mt.Shaper = newShaper()
	mt.Face = uiFace + ", Go"
	mt.TextSize = unit.Sp(13)
	t := &Theme{Theme: mt}
	t.SetName(name)
	return t
}

// SetName switches palettes.
func (t *Theme) SetName(name string) {
	if name == "Light" {
		t.P = LightPalette()
	} else {
		t.P = DarkPalette()
	}
	t.Palette.Bg = t.P.Window
	t.Palette.Fg = t.P.Text
	t.Palette.ContrastBg = t.P.Accent
	t.Palette.ContrastFg = rgb(0xffffff)
}

// Name is "Dark" or "Light".
func (t *Theme) Name() string {
	if t.P.Dark {
		return "Dark"
	}
	return "Light"
}

// Label is a themed label.
func (t *Theme) Label(size unit.Sp, txt string, col color.NRGBA) material.LabelStyle {
	l := material.Label(t.Theme, size, txt)
	l.Color = col
	return l
}

// Bold returns l in semibold.
func Bold(l material.LabelStyle) material.LabelStyle {
	l.Font.Weight = font.SemiBold
	return l
}
