//go:build windows

package ui

import (
	"gioui.org/app"
	"gioui.org/io/event"

	"silverspoon/assets"
	"silverspoon/internal/platform"
)

// setWindowIcon applies the app icon once Gio reports the window handle.
func setWindowIcon(e event.Event) {
	if v, ok := e.(app.Win32ViewEvent); ok && v.HWND != 0 {
		platform.SetWindowIcon(v.HWND, assets.Icon)
	}
}
