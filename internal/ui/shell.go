package ui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"silverspoon/internal/engine"
	"silverspoon/internal/providers"
	"silverspoon/internal/scheduler"
	"silverspoon/internal/store"
)

// Views in the sidebar. Every task is in exactly one of active, queued,
// done and errors, so their counts add up to all.
const (
	viewAll    = "all"
	viewActive = "active"
	viewQueued = "queued"
	viewDone   = "done"
	viewErrors = "errors"
)

func taskView(status string) string {
	switch {
	case strings.Contains(status, "Error") || status == engine.StCaptchaTimeout:
		return viewErrors
	case status == engine.StCompleted || status == engine.StExtracted || status == engine.StExtractCanceled:
		return viewDone
	case status == engine.StDownloading || status == engine.StStarting || status == engine.StSolving ||
		status == engine.StWaitingUser || status == engine.StExtracting || status == engine.StPausing ||
		status == engine.StResolving:
		return viewActive
	}
	return viewQueued
}

type navItem struct {
	key, label string
	icon       *widget.Icon
	click      widget.Clickable
}

type shell struct {
	filter  string // a view key, or "batch:<folder>"
	search  widget.Editor
	nav     []*navItem
	batches map[string]*widget.Clickable

	addBtn, schedBtn, settingsBtn, themeBtn, helpBtn widget.Clickable
	clearBtn, closeSel                               widget.Clickable
	schedLink                                        widget.Clickable
	selBtns                                          map[string]*widget.Clickable
	sidebarList                                      widget.List
}

func newShell() *shell {
	s := &shell{filter: viewAll, batches: map[string]*widget.Clickable{}, selBtns: map[string]*widget.Clickable{}}
	s.search.SingleLine = true
	s.sidebarList.Axis = layout.Vertical
	s.nav = []*navItem{
		{key: viewAll, label: "All downloads", icon: icAll},
		{key: viewActive, label: "Downloading", icon: icDownload},
		{key: viewQueued, label: "Queued & paused", icon: icQueue},
		{key: viewDone, label: "Completed", icon: icDone},
		{key: viewErrors, label: "Errors", icon: icError},
	}
	return s
}

// filtered applies the sidebar view and the search box.
func (a *App) filtered() []engine.View {
	q := strings.ToLower(strings.TrimSpace(a.sh.search.Text()))
	out := make([]engine.View, 0, len(a.views))
	for _, v := range a.views {
		switch {
		case a.sh.filter == viewAll:
		case strings.HasPrefix(a.sh.filter, "batch:"):
			if v.FolderName != strings.TrimPrefix(a.sh.filter, "batch:") {
				continue
			}
		case taskView(v.Status) != a.sh.filter:
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(v.Filename), q) && !strings.Contains(strings.ToLower(v.FolderName), q) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func (a *App) viewTitle() string {
	if strings.HasPrefix(a.sh.filter, "batch:") {
		return strings.TrimPrefix(a.sh.filter, "batch:")
	}
	for _, n := range a.sh.nav {
		if n.key == a.sh.filter {
			return n.label
		}
	}
	return "Downloads"
}

// --- sidebar -----------------------------------------------------------------

func (a *App) layoutSidebar(gtx C) D {
	th := a.th
	w := gtx.Dp(232)
	gtx.Constraints = layout.Exact(image.Pt(w, gtx.Constraints.Max.Y))
	fillRect(gtx, image.Rectangle{Max: gtx.Constraints.Max}, th.P.Sidebar)
	fillRect(gtx, image.Rect(w-gtx.Dp(1), 0, w, gtx.Constraints.Max.Y), th.P.Border)

	counts := map[string]int{viewAll: len(a.views)}
	for _, v := range a.views {
		counts[taskView(v.Status)]++
	}
	for _, n := range a.sh.nav {
		for n.click.Clicked(gtx) {
			a.setFilter(n.key)
		}
	}
	for a.sh.schedBtn.Clicked(gtx) {
		a.openScheduler(nil, "entire queue")
	}
	for a.sh.settingsBtn.Clicked(gtx) {
		a.openSettings()
	}
	for a.sh.themeBtn.Clicked(gtx) {
		if th.P.Dark {
			a.switchTheme("Light")
		} else {
			a.switchTheme("Dark")
		}
	}
	for a.sh.helpBtn.Clicked(gtx) {
		a.openHelpMenu()
	}
	order, groups := group(a.views)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Brand.
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: unit.Dp(16), Bottom: unit.Dp(14), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						if a.logo.Size().X == 0 {
							return D{}
						}
						s := gtx.Dp(26)
						img := widget.Image{Src: a.logo, Fit: widget.Contain}
						gtx.Constraints = layout.Exact(image.Pt(s, s))
						return img.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return Bold(th.Label(unit.Sp(15), "SilverSpoon", th.P.Text)).Layout(gtx) }),
							layout.Rigid(th.Label(unit.Sp(11), engine.Version, th.P.TextSubtle).Layout),
						)
					}),
				)
			})
		}),
		// Views and batches scroll together.
		layout.Flexed(1, func(gtx C) D {
			children := []layout.Widget{}
			for _, n := range a.sh.nav {
				n := n
				children = append(children, func(gtx C) D {
					return a.navRow(gtx, &n.click, n.icon, n.label, counts[n.key], a.sh.filter == n.key, nil)
				})
			}
			if len(order) > 0 {
				children = append(children, func(gtx C) D {
					return layout.Inset{Top: unit.Dp(18), Bottom: unit.Dp(6), Left: unit.Dp(20)}.Layout(gtx, func(gtx C) D {
						return th.SectionTitle(gtx, "Batches")
					})
				})
			}
			for _, f := range order {
				f := f
				b := a.sh.batches[f]
				if b == nil {
					b = new(widget.Clickable)
					a.sh.batches[f] = b
				}
				for b.Clicked(gtx) {
					a.setFilter("batch:" + f)
					a.table.Expand(f)
				}
				row := batchRow(f, groups[f])
				children = append(children, func(gtx C) D {
					return a.navRow(gtx, b, icFolder, f, len(groups[f]), a.sh.filter == "batch:"+f, &row)
				})
			}
			return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
				return a.sh.sidebarList.Layout(gtx, len(children), func(gtx C, i int) D { return children[i](gtx) })
			})
		}),
		// Footer.
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
				sched := a.Settings().Schedule
				desc := ""
				if sched != nil {
					desc = scheduler.Describe(*sched)
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(th.Divider),
					layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx C) D {
						sub := desc
						if sub == "" {
							sub = "Off"
						}
						return a.footerRow(gtx, &a.sh.schedBtn, icAlarm, "Scheduler", sub)
					}),
					layout.Rigid(func(gtx C) D { return a.footerRow(gtx, &a.sh.settingsBtn, icSettings, "Settings", "") }),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
							themeIc := icLight
							if !th.P.Dark {
								themeIc = icDark
							}
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx C) D { return th.IconButton(gtx, &a.sh.themeBtn, themeIc, th.P.TextMuted, unit.Dp(32)) }),
								layout.Rigid(func(gtx C) D { return th.IconButton(gtx, &a.sh.helpBtn, icHelp, th.P.TextMuted, unit.Dp(32)) }),
							)
						})
					}),
				)
			})
		}),
	)
}

func (a *App) navRow(gtx C, b *widget.Clickable, ic *widget.Icon, label string, count int, active bool, batch *Row) D {
	th := a.th
	return b.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		rec := op.Record(gtx.Ops)
		fg, icCol := th.P.TextMuted, th.P.TextSubtle
		if active {
			fg, icCol = th.P.Text, th.P.AccentText
		}
		d := layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return IconAt(gtx, ic, icCol, unit.Dp(17)) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
						layout.Flexed(1, func(gtx C) D {
							w := font.Normal
							if active {
								w = font.Medium
							}
							return th.Ellipsized(gtx, label, unit.Sp(13), fg, w, text.Start)
						}),
						layout.Rigid(func(gtx C) D {
							return th.Ellipsized(gtx, itoa(count), unit.Sp(12), th.P.TextSubtle, font.Normal, text.End)
						}),
					)
				}),
				layout.Rigid(func(gtx C) D {
					if batch == nil {
						return D{}
					}
					return layout.Inset{Top: unit.Dp(6), Left: unit.Dp(27)}.Layout(gtx, func(gtx C) D {
						return th.thinBar(gtx, *batch)
					})
				}),
			)
		})
		call := rec.Stop()
		switch {
		case active:
			box(gtx, image.Rectangle{Max: d.Size}, gtx.Dp(7), th.P.AccentSoft, colorT{})
		case b.Hovered():
			box(gtx, image.Rectangle{Max: d.Size}, gtx.Dp(7), th.P.Raised, colorT{})
		}
		call.Add(gtx.Ops)
		pointerCursor(gtx, d.Size, pointer.CursorPointer)
		return d
	})
}

func (a *App) footerRow(gtx C, b *widget.Clickable, ic *widget.Icon, label, sub string) D {
	th := a.th
	return b.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		rec := op.Record(gtx.Ops)
		d := layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return IconAt(gtx, ic, th.P.TextSubtle, unit.Dp(17)) }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return th.Ellipsized(gtx, label, unit.Sp(13), th.P.TextMuted, font.Normal, text.Start) }),
						layout.Rigid(func(gtx C) D {
							if sub == "" {
								return D{}
							}
							return th.Ellipsized(gtx, sub, unit.Sp(11), th.P.TextSubtle, font.Normal, text.Start)
						}),
					)
				}),
			)
		})
		call := rec.Stop()
		if b.Hovered() {
			box(gtx, image.Rectangle{Max: d.Size}, gtx.Dp(7), th.P.Raised, colorT{})
		}
		call.Add(gtx.Ops)
		pointerCursor(gtx, d.Size, pointer.CursorPointer)
		return d
	})
}

func (a *App) setFilter(f string) {
	a.sh.filter = f
	a.table.ClearSelection()
	a.table.list.Position = layout.Position{}
}

func (a *App) openHelpMenu() {
	items := []*MenuItem{
		Item("Welcome & shortcuts", func() { a.showWelcome(true) }),
		Item("Check for updates...", func() { a.checkForUpdates(true) }),
		Sep(),
		Item("GitHub repository", func() { openURL(RepoURL) }),
		Item("Report an issue", func() { openURL(IssuesURL) }),
		Item("Contributing guide", a.showContributing),
		Sep(),
		Item("About SilverSpoon", a.showAbout),
		Item("Donate", func() { openURL(DonateURL) }),
	}
	a.menu = &Menu{Items: items, At: a.mouse, open: true}
}

// --- toolbar -------------------------------------------------------------------

func (a *App) layoutToolbar(gtx C) D {
	th := a.th
	for a.sh.addBtn.Clicked(gtx) {
		a.openAddSheet("")
	}
	_, _, _, speed := a.counts()
	h := gtx.Dp(64)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	fillRect(gtx, image.Rect(0, h-gtx.Dp(1), gtx.Constraints.Max.X, h), th.P.Border)
	n := len(a.filtered())
	return layout.Inset{Left: unit.Dp(22), Right: unit.Dp(18)}.Layout(gtx, func(gtx C) D {
		return vcenter(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return th.Ellipsized(gtx, a.viewTitle(), unit.Sp(18), th.P.Text, font.SemiBold, text.Start)
						}),
						layout.Rigid(func(gtx C) D {
							s := fmt.Sprintf("%d item", n)
							if n != 1 {
								s += "s"
							}
							return th.Label(unit.Sp(12), s, th.P.TextSubtle).Layout(gtx)
						}),
					)
				}),
				// Search.
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(260), gtx.Dp(260)
					border := th.P.Border
					if gtx.Focused(&a.sh.search) {
						border = th.P.Focus
					}
					return background(gtx, unit.Dp(8), th.P.Input, border,
						layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)},
						func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx C) D { return IconAt(gtx, icSearch, th.P.TextSubtle, unit.Dp(16)) }),
								layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
								layout.Flexed(1, func(gtx C) D {
									e := editorStyle(th, &a.sh.search, "Search downloads")
									return e.Layout(gtx)
								}),
							)
						})
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				// Speed chip with sparkline.
				layout.Rigid(func(gtx C) D {
					return background(gtx, unit.Dp(8), th.P.Surface, th.P.Border,
						layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)},
						func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx C) D { return IconAt(gtx, icDownload, th.P.Success, unit.Dp(16)) }),
								layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
								layout.Rigid(func(gtx C) D {
									gtx.Constraints.Min.X = gtx.Dp(78)
									return th.Ellipsized(gtx, fmt.Sprintf("%.2f MB/s", speed), unit.Sp(13), th.P.Text, font.SemiBold, text.Start)
								}),
								layout.Rigid(func(gtx C) D {
									gtx.Constraints.Max.X = gtx.Dp(84)
									return th.SpeedGraph(gtx, a.graph, unit.Dp(24))
								}),
							)
						})
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx C) D { return th.IconLabelButton(gtx, &a.sh.addBtn, icAdd, "Add downloads", th.P.Primary) }),
			)
		})
	})
}

// --- selection bar ---------------------------------------------------------------

func (a *App) selBtn(name string) *widget.Clickable {
	b := a.sh.selBtns[name]
	if b == nil {
		b = new(widget.Clickable)
		a.sh.selBtns[name] = b
	}
	return b
}

type selAction struct {
	label string
	icon  *widget.Icon
	do    func()
	kind  ButtonKind
}

func (a *App) selectionActions() []selAction {
	return []selAction{
		{"Start", icPlay, a.startSelected, KindNormal},
		{"Pause", icPause, a.pauseSelected, KindNormal},
		{"Cancel", icStop, a.cancelSelected, KindNormal},
		{"Retry", icRetry, a.retrySelected, KindNormal},
		{"Redownload", icRedo, a.forceRedownload, KindNormal},
		{"Extract", icUnarchive, a.extractSelected, KindNormal},
		{"Copy error", icCopy, a.copyErrorDetails, KindNormal},
		{"Delete", icDelete, a.deleteSelected, KindDanger},
	}
}

// layoutSelectionBar floats over the bottom of the list while tasks are
// selected (Gmail/Linear style), replacing a permanent button row.
func (a *App) layoutSelectionBar(gtx C, n int) D {
	th := a.th
	acts := a.selectionActions()
	for _, ac := range acts {
		for a.selBtn(ac.label).Clicked(gtx) {
			ac.do()
		}
	}
	for a.sh.closeSel.Clicked(gtx) {
		a.table.ClearSelection()
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
				return Bold(th.Label(unit.Sp(13), fmt.Sprintf("%d selected", n), th.P.Text)).Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx C) D {
			h := gtx.Dp(20)
			fillRect(gtx, image.Rect(0, 0, gtx.Dp(1), h), th.P.BorderStrong)
			return D{Size: image.Pt(gtx.Dp(9), h)}
		}),
	}
	for _, ac := range acts {
		ac := ac
		children = append(children, layout.Rigid(func(gtx C) D {
			col := th.P.Ghost
			if ac.kind == KindDanger {
				col.Fg = th.P.Danger
			}
			return th.button(gtx, a.selBtn(ac.label), ac.icon, ac.label, col, true)
		}))
	}
	children = append(children, layout.Rigid(func(gtx C) D {
		return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
			return th.IconButton(gtx, &a.sh.closeSel, icClose, th.P.TextMuted, unit.Dp(28))
		})
	}))
	m := op.Record(gtx.Ops)
	gtx.Constraints.Min = image.Point{}
	d := background(gtx, unit.Dp(12), th.P.Overlay, th.P.BorderStrong,
		layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(6)},
		func(gtx C) D { return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...) })
	call := m.Stop()
	sh := image.Rectangle{Max: d.Size}.Add(image.Pt(0, gtx.Dp(3))).Inset(-gtx.Dp(2))
	box(gtx, sh, gtx.Dp(14), withAlpha(rgb(0), 0x40), colorT{})
	call.Add(gtx.Ops)
	return d
}

// --- status bar -------------------------------------------------------------------

func (a *App) layoutStatusBar(gtx C) D {
	th := a.th
	if a.autoExtract.Update(gtx) {
		a.eng.AutoExtract.Store(a.autoExtract.Value)
	}
	for a.sh.clearBtn.Clicked(gtx) {
		a.eng.ClearFinished()
		a.refresh()
	}
	for a.sh.schedLink.Clicked(gtx) {
		a.openScheduler(nil, "entire queue")
	}
	active, queued, done, _ := a.counts()
	h := gtx.Dp(34)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	fillRect(gtx, image.Rect(0, 0, gtx.Constraints.Max.X, gtx.Dp(1)), th.P.Border)
	dot := func(c colorT) layout.FlexChild {
		return layout.Rigid(func(gtx C) D {
			s := gtx.Dp(7)
			box(gtx, image.Rect(0, 0, s, s), s/2, c, colorT{})
			return D{Size: image.Pt(s, s)}
		})
	}
	stat := func(c colorT, txt string) []layout.FlexChild {
		return []layout.FlexChild{dot(c), layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(th.Label(unit.Sp(12), txt, th.P.TextMuted).Layout),
			layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout)}
	}
	children := []layout.FlexChild{}
	children = append(children, stat(th.P.Info, fmt.Sprintf("%d active", active))...)
	children = append(children, stat(th.P.TextSubtle, fmt.Sprintf("%d queued", queued))...)
	children = append(children, stat(th.P.Success, fmt.Sprintf("%d done", done))...)
	if s := a.Settings().Schedule; s != nil && s.Enabled {
		children = append(children, layout.Rigid(func(gtx C) D {
			return a.sh.schedLink.Layout(gtx, func(gtx C) D {
				d := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return th.ScheduleClock(gtx, &a.clock, s.Start, a.eng.WindowActive()) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(th.Label(unit.Sp(12), scheduler.Describe(*s), th.P.TextMuted).Layout),
				)
				pointerCursor(gtx, d.Size, pointer.CursorPointer)
				return d
			})
		}))
	}
	children = append(children,
		layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
		layout.Rigid(func(gtx C) D { return th.Switch(gtx, &a.autoExtract, "Auto-extract") }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout),
		layout.Rigid(func(gtx C) D { return th.button(gtx, &a.sh.clearBtn, icClear, "Clear completed", th.P.Ghost, true) }),
	)
	return layout.Inset{Left: unit.Dp(18), Right: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
		return vcenter(gtx, func(gtx C) D { return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...) })
	})
}

// --- add downloads sheet ------------------------------------------------------------

// openAddSheet collects links, the save folder and the batch name in one
// place (the Python app's ingest card plus its folder-name prompt).
func (a *App) openAddSheet(initial string) {
	th := a.th
	var links, dir, folder widget.Editor
	dir.SingleLine, folder.SingleLine, folder.Submit = true, true, true
	dir.SetText(a.Settings().DefaultSaveDir)
	if a.lastSaveDir != "" {
		dir.SetText(a.lastSaveDir)
	}
	links.SetText(initial)
	var paste, imp, browse widget.Clickable
	var startNow widget.Bool
	// Checked by default; the choice is remembered.
	startNow.Value = a.Settings().Bool("start_immediately", true)
	folderEdited := false
	lastSuggest := ""
	focused := false

	appendText := func(txt string) {
		if txt == "" {
			return
		}
		cur := links.Text()
		if strings.TrimSpace(cur) != "" && !strings.HasSuffix(cur, "\n") {
			cur += "\n"
		}
		links.SetText(cur + txt)
		links.SetCaret(links.Len(), links.Len())
	}

	var d *Dialog
	add := func() bool {
		ls := engine.ParseLinks(links.Text())
		name := strings.TrimSpace(folder.Text())
		if len(ls) == 0 {
			a.message("No links", "Paste at least one http(s) link.")
			return false
		}
		if name == "" {
			a.message("Folder name needed", "Enter a folder name for these files.")
			return false
		}
		saveDir := absDir(dir.Text())
		a.lastSaveDir = saveDir
		uids := a.eng.AddLinks(ls, saveDir, name)
		if a.Settings().Bool("start_immediately", true) != startNow.Value {
			a.saveSettings(func(st *store.Settings) { st.SetBool("start_immediately", startNow.Value) })
		}
		if startNow.Value {
			a.eng.StartTasks(uids)
		}
		a.table.Expand(name)
		a.refresh()
		return true
	}
	body := func(gtx C) D {
		for range keyPressed(gtx, key.Filter{Focus: &links, Name: "V", Required: key.ModShortcut}) {
			go func() {
				html, plain := clipboardText()
				txt := linksFromClipboard(html, plain)
				a.post(func() { appendText(txt) })
			}()
		}
		for paste.Clicked(gtx) {
			go func() {
				html, plain := clipboardText()
				txt := linksFromClipboard(html, plain)
				a.post(func() { appendText(txt) })
			}()
		}
		for imp.Clicked(gtx) {
			go func() {
				if txt, ok := readPickedFile(); ok {
					a.post(func() { appendText(txt) })
				}
			}()
		}
		for browse.Clicked(gtx) {
			start := dir.Text()
			go func() {
				if p, ok := pickFolder(start); ok {
					a.post(func() { dir.SetText(p) })
				}
			}()
		}
		for {
			ev, ok := folder.Update(gtx)
			if !ok {
				break
			}
			switch ev.(type) {
			case widget.ChangeEvent:
				if folder.Text() != lastSuggest {
					folderEdited = true
				}
			case widget.SubmitEvent:
				if add() {
					d.Close()
				}
			}
		}
		ls := engine.ParseLinks(links.Text())
		if !folderEdited && len(ls) > 0 {
			if s := engine.SuggestFolder(ls[0]); s != folder.Text() {
				lastSuggest = s
				folder.SetText(s)
			}
		}
		if !focused {
			gtx.Execute(key.FocusCmd{Tag: &links})
			focused = true
		}
		hosts := hostSummary(ls)
		label := func(txt string) layout.FlexChild {
			return layout.Rigid(func(gtx C) D {
				return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, th.Label(unit.Sp(12), txt, th.P.TextMuted).Layout)
			})
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			label("Links"),
			layout.Rigid(func(gtx C) D {
				return th.Field(gtx, &links, "Paste FuckingFast, DataNodes, FileKeeper, BZZHR or any direct download links — one per line", 150)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return th.button(gtx, &paste, icPaste, "Paste", th.P.Button, true) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx C) D { return th.button(gtx, &imp, icImport, "Import .txt", th.P.Button, true) }),
					layout.Flexed(1, func(gtx C) D {
						txt := "No links yet"
						col := th.P.TextSubtle
						if len(ls) > 0 {
							txt = fmt.Sprintf("%d link(s) detected", len(ls))
							if hosts != "" {
								txt += "  ·  " + hosts
							}
							col = th.P.Success
						}
						return th.Ellipsized(gtx, txt, unit.Sp(12), col, font.Medium, text.End)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(18)}.Layout),
			label("Save to"),
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D { return th.Field(gtx, &dir, "Download folder", 0) }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx C) D { return th.button(gtx, &browse, icFolderOp, "Browse", th.P.Button, false) }),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			label("Batch folder — groups main game and optional files together"),
			layout.Rigid(func(gtx C) D { return th.Field(gtx, &folder, "Folder name", 0) }),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx C) D {
				return th.Ellipsized(gtx, "→ "+joinPath(absDir(dir.Text()), strings.TrimSpace(folder.Text())), unit.Sp(12), th.P.TextSubtle, font.Normal, text.Start)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			layout.Rigid(func(gtx C) D { return th.CheckBox(gtx, &startNow, "Start downloading right away") }),
		)
	}
	d = &Dialog{Title: "Add downloads", Width: 640, Default: -1, Cancel: 1, Body: body,
		Buttons: []*DialogButton{Btn("Add to queue", KindPrimary, add), Btn("Cancel", KindNormal, nil)}}
	a.dialogs = append(a.dialogs, d)
}

// hostSummary is "FuckingFast ×12, DataNodes ×2".
func hostSummary(links []string) string {
	counts := map[string]int{}
	var order []string
	for _, l := range links {
		h := "Direct"
		switch {
		case providers.NeedsFormResolution(l):
			h = "FileKeeper"
		case strings.Contains(l, "fuckingfast"):
			h = "FuckingFast"
		case strings.Contains(l, "datanodes"):
			h = "DataNodes"
		case strings.Contains(l, "bzzhr"):
			h = "BZZHR"
		}
		if counts[h] == 0 {
			order = append(order, h)
		}
		counts[h]++
	}
	parts := make([]string, len(order))
	for i, h := range order {
		parts[i] = fmt.Sprintf("%s ×%d", h, counts[h])
	}
	return strings.Join(parts, ", ")
}
