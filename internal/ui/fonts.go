package ui

import (
	"os"
	"path/filepath"
	"runtime"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

// uiFace is the typeface the theme asks for.
const uiFace = "Segoe UI"

// fontCollection loads only the Segoe UI weights the UI uses (Windows), with
// the Go fonts as fallback. Letting the shaper scan every installed system
// font costs tens of megabytes of memory for no visible gain.
func fontCollection() []font.FontFace {
	faces := gofont.Collection()
	if runtime.GOOS != "windows" {
		return faces
	}
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	for _, f := range []struct {
		file   string
		weight font.Weight
	}{
		{"segoeui.ttf", font.Normal},
		{"seguisb.ttf", font.SemiBold},
		{"segoeuib.ttf", font.Bold},
		{"seguibl.ttf", font.ExtraBold},
	} {
		b, err := os.ReadFile(filepath.Join(dir, f.file))
		if err != nil {
			continue
		}
		parsed, err := opentype.ParseCollection(b)
		if err != nil {
			continue
		}
		for _, p := range parsed {
			p.Font.Typeface = uiFace
			p.Font.Weight = f.weight
			faces = append(faces, p)
		}
	}
	return faces
}

func newShaper() *text.Shaper {
	return text.NewShaper(text.NoSystemFonts(), text.WithCollection(fontCollection()))
}
