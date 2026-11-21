// Package assets embeds the images the UI shows.
package assets

import _ "embed"

// Logo is the splash/about image.
//
//go:embed SilverSpoon.png
var Logo []byte
