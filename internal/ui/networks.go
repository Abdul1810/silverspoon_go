package ui

import (
	"fmt"
	"image"
	"sort"
	"strings"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"silverspoon/internal/netmon"
	"silverspoon/internal/store"
)

// Settings keys for the network options. The Python app doesn't know them
// and keeps them untouched.
const (
	KeyAllNetworks = "use_all_networks"
	KeyExcludedNet = "excluded_networks"
)

// SetNetworks gives the UI the network monitor to show.
func (a *App) SetNetworks(m *netmon.Monitor) { a.nets = m }

func netIcon(k netmon.Kind) *widget.Icon {
	switch k {
	case netmon.KindEthernet:
		return icEthernet
	case netmon.KindWiFi:
		return icWiFi
	case netmon.KindTether:
		return icTether
	case netmon.KindCellular:
		return icCellular
	}
	return icNetOther
}

func kindLabel(k netmon.Kind) string {
	switch k {
	case netmon.KindEthernet:
		return "Ethernet"
	case netmon.KindWiFi:
		return "Wi-Fi"
	case netmon.KindTether:
		return "USB tethering"
	case netmon.KindCellular:
		return "Mobile broadband"
	}
	return "Network"
}

func rateText(bps float64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.2f MB/s", bps/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.0f KB/s", bps/(1<<10))
	case bps > 0:
		return "<1 KB/s"
	}
	return "idle"
}

// netUsed reports whether downloads go through n right now.
func (a *App) netUsed(n netmon.Net) bool {
	if !n.Online {
		return false
	}
	s := a.Settings()
	if s.Bool(KeyAllNetworks, true) {
		return !n.Excluded
	}
	list := "," + strings.ReplaceAll(s.BindAddresses, " ", "") + ","
	return strings.Contains(list, ","+n.IP+",")
}

// usingNetworks counts the networks downloads spread over, and the online ones.
func (a *App) usingNetworks() (used, online int) {
	if a.nets == nil {
		return 0, 0
	}
	for _, n := range a.nets.Snapshot() {
		if n.State != netmon.StateOffline {
			online++
			if a.netUsed(n) {
				used++
			}
		}
	}
	return used, online
}

func (a *App) toggleNetwork(name string) {
	ex := map[string]bool{}
	for _, n := range a.nets.Excluded() {
		ex[n] = true
	}
	ex[name] = !ex[name]
	var list []string
	for n, on := range ex {
		if on {
			list = append(list, n)
		}
	}
	sort.Strings(list)
	a.nets.SetExcluded(list)
	a.saveSettings(func(s *store.Settings) { s.SetStrings(KeyExcludedNet, list) })
}

// layoutNetworks is the sidebar panel: every network with internet, how
// many download connections it carries and how much bandwidth it gives.
func (a *App) layoutNetworks(gtx C) D {
	th := a.th
	if a.nets == nil {
		return D{}
	}
	nets := a.nets.Snapshot()
	if a.netBtns == nil {
		a.netBtns = map[string]*widget.Clickable{}
	}
	var total float64
	for _, n := range nets {
		if a.netUsed(n) {
			total += n.BytesPerSec
		}
	}
	auto := a.Settings().Bool(KeyAllNetworks, true)
	children := []layout.FlexChild{
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(4), Left: unit.Dp(12), Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D { return th.SectionTitle(gtx, "Networks") }),
					layout.Rigid(func(gtx C) D {
						mode := "Manual IPs"
						if auto {
							mode = "Auto"
						}
						return th.Label(unit.Sp(11), mode, th.P.TextSubtle).Layout(gtx)
					}),
				)
			})
		}),
	}
	if len(nets) == 0 {
		children = append(children, layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: unit.Dp(12), Bottom: unit.Dp(6)}.Layout(gtx,
				th.Label(unit.Sp(12), "No connected network found", th.P.TextSubtle).Layout)
		}))
	}
	a.hoverNet = nil
	for _, n := range nets {
		n := n
		b := a.netBtns[n.Name]
		if b == nil {
			b = new(widget.Clickable)
			a.netBtns[n.Name] = b
		}
		for b.Clicked(gtx) {
			if auto {
				a.toggleNetwork(n.Name)
			} else {
				a.message("Manual IP list in use", "Downloads use the IPs listed in Settings → Download via IPs.\n\n"+
					"Turn on \"Use all connected networks\" in Settings to pick networks here.")
			}
		}
		if b.Hovered() {
			nn := n
			a.hoverNet = &nn
		}
		children = append(children, layout.Rigid(func(gtx C) D { return a.networkRow(gtx, b, n, total, auto) }))
	}
	return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func (a *App) networkRow(gtx C, b *widget.Clickable, n netmon.Net, total float64, auto bool) D {
	th := a.th
	used := a.netUsed(n)
	return b.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		rec := op.Record(gtx.Ops)
		iconCol, nameCol := th.P.Success, th.P.Text
		if n.Kind == netmon.KindTether {
			iconCol = th.P.Info
		}
		if !used {
			iconCol, nameCol = th.P.TextSubtle, th.P.TextMuted
		}
		ic := netIcon(n.Kind)
		if n.State == netmon.StateOffline {
			ic = icNetOff
		}
		reconnecting := n.State == netmon.StateReconnecting && !n.Excluded
		if reconnecting {
			iconCol, nameCol = th.P.Warning, th.P.Text
		}
		var status string
		switch {
		case reconnecting && n.Missing:
			status = "Reconnecting… (adapter resetting)"
		case reconnecting:
			status = "Reconnecting… (" + n.IP + ")"
		case n.State == netmon.StateOffline:
			status = "No internet"
		case !used && auto:
			status = "Off · click to use"
		case !used:
			status = "Not in the IP list"
		case n.Connections == 0:
			status = n.IP + " · " + kindLabel(n.Kind)
		default:
			c := "connections"
			if n.Connections == 1 {
				c = "connection"
			}
			status = fmt.Sprintf("%s · %d %s", n.IP, n.Connections, c)
		}
		d := layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return IconAt(gtx, ic, iconCol, unit.Dp(16)) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Flexed(1, func(gtx C) D {
							return th.Ellipsized(gtx, n.Name, unit.Sp(12), nameCol, font.Medium, text.Start)
						}),
						layout.Rigid(func(gtx C) D {
							col := th.P.TextSubtle
							if n.BytesPerSec > 0 && used {
								col = th.P.Success
							}
							txt := rateText(n.BytesPerSec)
							if reconnecting && n.BytesPerSec == 0 {
								txt, col = "…", th.P.Warning
							}
							return th.Ellipsized(gtx, txt, unit.Sp(12), col, font.SemiBold, text.End)
						}),
					)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Left: unit.Dp(24), Top: unit.Dp(2)}.Layout(gtx, func(gtx C) D {
						return th.Ellipsized(gtx, status, unit.Sp(11), th.P.TextSubtle, font.Normal, text.Start)
					})
				}),
				layout.Rigid(func(gtx C) D {
					if !used {
						return D{}
					}
					share := 0.0
					if total > 0 {
						share = n.BytesPerSec / total * 100
					}
					// The bar shows this network's share of the total speed.
					return layout.Inset{Left: unit.Dp(24), Top: unit.Dp(5)}.Layout(gtx, func(gtx C) D {
						return th.thinBar(gtx, Row{Progress: share, Status: "Downloading"})
					})
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

// networkTooltip explains a network row on hover.
func networkTooltip(n netmon.Net, total float64) string {
	s := n.Name + " — " + kindLabel(n.Kind)
	if n.Description != "" {
		s += "\n" + n.Description
	}
	s += "\nIP " + n.IP
	if n.Gateway != "" {
		s += ", gateway " + n.Gateway
	}
	if n.LinkMbps > 0 {
		s += fmt.Sprintf("\nLink speed %d Mbps", n.LinkMbps)
	}
	if total > 0 && n.BytesPerSec > 0 {
		s += fmt.Sprintf("\nGiving %.0f%% of the current download speed", n.BytesPerSec/total*100)
	}
	s += "\nDownloaded " + HumanBytes(n.TotalBytes) + " through it this session"
	return s
}
