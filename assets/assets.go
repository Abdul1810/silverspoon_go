// Package assets embeds the images the UI shows.
package assets

import _ "embed"

// Logo is the splash/sidebar image (1024×1024 PNG).
//
//go:embed SilverSpoon.png
var Logo []byte

// Icon is the window/taskbar icon (multi-size ICO, 16–256 px).
//
//go:embed SilverSpoon.ico
var Icon []byte
