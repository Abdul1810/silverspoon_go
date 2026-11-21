package ui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"silverspoon/internal/platform"
	"silverspoon/internal/scheduler"
	"silverspoon/internal/store"
	"silverspoon/internal/transport"
)

var systemClose = system.ActionClose

// formRow lays out "label   control" with a fixed label column.
func (a *App) formRow(label, hint string, control layout.Widget) layout.FlexChild {
	return layout.Rigid(func(gtx C) D {
		return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Dp(210)
					gtx.Constraints.Max.X = gtx.Constraints.Min.X
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(a.th.Label(unit.Sp(13), label, a.th.P.Text).Layout),
						layout.Rigid(func(gtx C) D {
							if hint == "" {
								return D{}
							}
							return a.th.Label(unit.Sp(11), hint, a.th.P.TextSubtle).Layout(gtx)
						}),
					)
				}),
				layout.Flexed(1, control),
			)
		})
	})
}

func (a *App) sectionHeading(txt string) layout.FlexChild {
	return layout.Rigid(func(gtx C) D {
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
			return a.th.SectionTitle(gtx, txt)
		})
	})
}

func (a *App) openSettings() {
	s := a.Settings()
	var (
		theme                                   = Segmented{Value: s.Theme}
		dir, bind                               widget.Editor
		browse, detect                          widget.Clickable
		workers                                 = Stepper{Min: 1, Max: 10}
		conns                                   = Stepper{Min: 1, Max: 16}
		bw                                      = Stepper{Min: 0, Max: 1000}
		captcha                                 = Stepper{Min: 5, Max: 120}
		showSolver, extract, retry, skipConfirm widget.Bool
		resetWidths, resetWelcome               bool
	)
	dir.SingleLine, bind.SingleLine = true, true
	load := func(s store.Settings) {
		theme.Value = s.Theme
		dir.SetText(s.DefaultSaveDir)
		bind.SetText(s.BindAddresses)
		workers.Set(s.MaxWorkers)
		conns.Set(s.MaxConnections)
		bw.Set(s.BandwidthLimit)
		captcha.Set(s.CaptchaTimeout)
		showSolver.Value, extract.Value = s.ShowSolverBrowser, s.ExtractAfterDownload
		retry.Value, skipConfirm.Value = s.AutoRetryErrors, s.SkipDeleteConfirmation
	}
	load(s)

	body := func(gtx C) D {
		for browse.Clicked(gtx) {
			start := dir.Text()
			go func() {
				if p, ok := platform.PickFolder("Select Save Directory", start); ok {
					a.post(func() { dir.SetText(p) })
				}
			}()
		}
		for detect.Clicked(gtx) {
			bind.SetText(strings.Join(transport.LocalIPv4Addresses(), ", "))
		}
		field := func(ed *widget.Editor, hint string) layout.Widget {
			return func(gtx C) D { return a.th.Field(gtx, ed, hint, 0) }
		}
		withButton := func(w layout.Widget, b *widget.Clickable, label string) layout.Widget {
			return func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, w),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx C) D { return a.th.Button(gtx, b, label, a.th.P.Button) }),
				)
			}
		}
		check := func(b *widget.Bool, label string) layout.Widget {
			return func(gtx C) D { return a.th.CheckBox(gtx, b, label) }
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			a.sectionHeading("General"),
			a.formRow("Application theme", "", func(gtx C) D { return a.th.Segmented(gtx, &theme, "Dark", "Light") }),
			a.formRow("Default save directory", "", withButton(field(&dir, "Download folder"), &browse, "Browse...")),
			a.sectionHeading("Downloads"),
			a.formRow("Max concurrent downloads", "", func(gtx C) D { return a.th.Stepper(gtx, &workers, "", "") }),
			a.formRow("Connections per download", "1 turns multi-connection off", func(gtx C) D { return a.th.Stepper(gtx, &conns, "", "") }),
			a.formRow("Download via IPs (rotate)", "Comma separated; blank = automatic", withButton(field(&bind, "blank = automatic (one network)"), &detect, "Detect")),
			a.formRow("Global bandwidth limit", "", func(gtx C) D { return a.th.Stepper(gtx, &bw, "MB/s", "Unlimited") }),
			a.formRow("Retry failed downloads", "", check(&retry, "Automatically, up to 3 times")),
			a.sectionHeading("CAPTCHA & Extraction"),
			a.formRow("CAPTCHA solve timeout", "", func(gtx C) D { return a.th.Stepper(gtx, &captcha, "seconds", "") }),
			a.formRow("CAPTCHA browser window", "Applies the next time it starts", check(&showSolver, "Show the window")),
			a.formRow("Auto-extract", "", check(&extract, "Extract after download by default")),
			a.formRow("Delete confirmation", "", check(&skipConfirm, "Skip delete confirmation")),
		)
	}

	save := func() bool {
		a.saveSettings(func(st *store.Settings) {
			st.Theme = theme.Value
			st.DefaultSaveDir = strings.TrimSpace(dir.Text())
			st.MaxWorkers = workers.Value()
			st.MaxConnections = conns.Value()
			st.BindAddresses = strings.TrimSpace(bind.Text())
			st.SetBandwidthLimit(bw.Value())
			st.CaptchaTimeout = captcha.Value()
			st.ShowSolverBrowser = showSolver.Value
			st.ExtractAfterDownload = extract.Value
			st.AutoRetryErrors = retry.Value
			st.SkipDeleteConfirmation = skipConfirm.Value
			if resetWidths {
				st.ColumnWidths = map[string]int{}
			}
			if resetWelcome {
				st.ShowWarningDialog = true
			}
		})
		ns := a.Settings()
		a.th.SetName(ns.Theme)
		if a.sv != nil {
			a.sv.TokenTimeout, a.sv.ShowWindow = ns.CaptchaTimeout, ns.ShowSolverBrowser
		}
		if resetWidths {
			a.table.widths = map[string]int{}
		}
		a.lastSaveDir = ""
		a.autoExtract.Value = ns.ExtractAfterDownload
		a.eng.AutoExtract.Store(ns.ExtractAfterDownload)
		return true
	}
	reset := func() bool {
		a.confirm("Confirm Reset", "Are you sure you want to reset all settings to their default values? (Includes showing warnings and UI sizes)",
			"Reset", true, func() {
				d := store.Defaults()
				load(*d)
				resetWidths, resetWelcome = true, true
			})
		return false
	}
	a.dialogs = append(a.dialogs, &Dialog{Title: "Settings", Width: 620, Default: -1, Cancel: 2, Body: body,
		Buttons: []*DialogButton{
			Btn("Reset Defaults", KindNormal, reset),
			Btn("Save", KindPrimary, save),
			Btn("Cancel", KindNormal, nil),
		}})
}

// timePicker is hour / minute / AM-PM selection writing 24h "HH:mm".
type timePicker struct {
	hour, minute Stepper
	ampm         Segmented
}

func (tp *timePicker) set(hhmm string) {
	h, m, ap := scheduler.Split12h(hhmm)
	tp.hour = Stepper{Min: 1, Max: 12, Width: 46}
	tp.minute = Stepper{Min: 0, Max: 59, Width: 46}
	tp.hour.Set(h)
	tp.minute.Set(m)
	tp.ampm.Value = ap
}

func (tp *timePicker) get() string {
	return scheduler.Join24h(tp.hour.Value(), tp.minute.Value(), tp.ampm.Value)
}

func (a *App) timePicker(tp *timePicker) layout.Widget {
	return func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return a.th.Stepper(gtx, &tp.hour, "", "") }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(a.th.Label(unit.Sp(14), ":", a.th.P.Text).Layout),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx C) D { return a.th.Stepper(gtx, &tp.minute, "", "") }),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx C) D { return a.th.Segmented(gtx, &tp.ampm, "AM", "PM") }),
		)
	}
}

// openScheduler edits the off-peak window for the whole queue (targets nil)
// or for the given task uids.
func (a *App) openScheduler(targets []string, scope string) {
	cur := scheduler.DefaultSchedule()
	if s := a.Settings().Schedule; s != nil {
		cur = *s
	}
	var start, end timePicker
	start.set(cur.Start)
	end.set(cur.End)
	var rec widget.Enum
	rec.Value = "weekly"
	if cur.Recurrence == "once" {
		rec.Value = "once"
	}
	active := map[int]bool{}
	if cur.Days == nil {
		for i := 0; i < 7; i++ {
			active[i] = true
		}
	} else {
		for _, d := range *cur.Days {
			active[d] = true
		}
	}
	var days [7]widget.Bool
	for i := range days {
		days[i].Value = active[i]
	}
	var date widget.Editor
	date.SingleLine = true
	if cur.Date != "" {
		date.SetText(cur.Date)
	} else {
		date.SetText(time.Now().Format("2006-01-02"))
	}
	var wake, keepAwake widget.Bool
	wake.Value, keepAwake.Value = cur.WakeTimer, cur.KeepAwake
	names := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

	build := func(enabled bool) scheduler.Schedule {
		var ds []int
		for i := range days {
			if days[i].Value {
				ds = append(ds, i)
			}
		}
		if ds == nil {
			ds = []int{}
		}
		s := scheduler.Schedule{Enabled: enabled, Start: start.get(), End: end.get(), Recurrence: rec.Value,
			Days: &ds, Date: strings.TrimSpace(date.Text()), WakeTimer: wake.Value, KeepAwake: keepAwake.Value}
		if targets != nil {
			tg := append([]string(nil), targets...)
			s.Targets = &tg
		}
		return s
	}
	apply := func(s scheduler.Schedule) {
		a.saveSettings(func(st *store.Settings) { st.Schedule = &s })
		a.eng.SetSchedule(s)
		// (Un)register the Windows wake timer to match.
		go func() {
			if s.Enabled && s.WakeTimer {
				exe, args := LaunchCommand()
				ok, msg := scheduler.RegisterWakeTask(s, exe, args)
				title := "Wake Timer"
				if !ok {
					title = "Wake Timer Unavailable"
				}
				a.post(func() { a.message(title, msg) })
			} else {
				scheduler.UnregisterWakeTask()
			}
		}()
	}

	body := func(gtx C) D {
		weekly := rec.Value == "weekly"
		rec.Update(gtx)
		dayRow := func(gtx C) D {
			children := make([]layout.FlexChild, 7)
			for i := range days {
				i := i
				children[i] = layout.Rigid(func(gtx C) D {
					if !weekly {
						gtx = gtx.Disabled()
					}
					return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D { return a.th.CheckBox(gtx, &days[i], names[i]) })
				})
			}
			return layout.Flex{}.Layout(gtx, children...)
		}
		wakeLabel := "Wake the computer to run downloads"
		if runtime.GOOS != "windows" {
			wakeLabel += " (Windows only)"
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx,
					a.th.Label(unit.Sp(13), "Scheduling: "+scope, a.th.P.TextMuted).Layout)
			}),
			a.sectionHeading("Window"),
			a.formRow("Start", "", a.timePicker(&start)),
			a.formRow("End", "End before start = crosses midnight", a.timePicker(&end)),
			a.sectionHeading("Recurrence"),
			layout.Rigid(func(gtx C) D { return a.th.Radio(gtx, &rec, "weekly", "Repeat weekly on:") }),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx C) D { return layout.Inset{Left: unit.Dp(22)}.Layout(gtx, dayRow) }),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return a.th.Radio(gtx, &rec, "once", "Run once on:") }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx C) D {
						if weekly {
							gtx = gtx.Disabled()
						}
						gtx.Constraints.Max.X = gtx.Dp(130)
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return a.th.Field(gtx, &date, "YYYY-MM-DD", 0)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			a.sectionHeading("Power"),
			layout.Rigid(func(gtx C) D { return a.th.CheckBox(gtx, &wake, wakeLabel) }),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.th.CheckBox(gtx, &keepAwake, "Keep the computer awake while the window is open")
			}),
		)
	}

	add := func() bool {
		s := build(true)
		if s.Recurrence == "weekly" && len(*s.Days) == 0 {
			a.message("No days selected", "Pick at least one day, or choose Run once.")
			return false
		}
		if s.Recurrence == "once" {
			day, err := time.ParseInLocation("2006-01-02", s.Date, time.Local)
			if err != nil {
				a.message("Invalid Date", "Enter the date as YYYY-MM-DD.")
				return false
			}
			sh, sm, _ := scheduler.ParseHHMM(s.Start)
			eh, em, _ := scheduler.ParseHHMM(s.End)
			endDT := time.Date(day.Year(), day.Month(), day.Day(), eh, em, 0, 0, time.Local)
			if eh*60+em <= sh*60+sm {
				endDT = endDT.AddDate(0, 0, 1)
			}
			if !time.Now().Before(endDT) {
				a.message("Invalid Date", "The selected one-off schedule window has already passed.\nPlease select a current or future date.")
				return false
			}
		}
		apply(s)
		return true
	}
	buttons := []*DialogButton{Btn("Add Schedule", KindPrimary, add), Btn("Cancel", KindNormal, nil)}
	cancel := 1
	if cur.Enabled {
		buttons = append([]*DialogButton{Btn("Remove Schedule", KindDanger, func() bool {
			apply(build(false))
			return true
		})}, buttons...)
		cancel = 2
	}
	a.dialogs = append(a.dialogs, &Dialog{Title: "Download Scheduler", Width: 600, Default: -1, Cancel: cancel,
		Body: body, Buttons: buttons})
}

var _ = fmt.Sprint
