// Package fonts embeds the paper's typefaces, IM Fell English (roman,
// italic, small caps) and UnifrakturMaguntia, with their SIL Open Font
// License texts.
package fonts

import (
	"embed"
	"fmt"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// Files holds the four TTFs and the two OFL texts at its root.
//
//go:embed *.ttf OFL-*.txt
var Files embed.FS

// files maps the names Face accepts to their TTF.
var files = map[string]string{
	"fell":        "IMFeENrm28P.ttf",
	"fell-italic": "IMFeENit28P.ttf",
	"fell-sc":     "IMFeENsc28P.ttf",
	"fraktur":     "UnifrakturMaguntia-Book.ttf",
}

// Face returns the named typeface at size pixels (72 dpi, so one point
// is one pixel). Names: "fell", "fell-italic", "fell-sc", "fraktur".
func Face(name string, size float64) (font.Face, error) {
	file, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("fonts: unknown face %q", name)
	}
	if size <= 0 {
		return nil, fmt.Errorf("fonts: size must be positive, got %v", size)
	}
	data, err := Files.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("fonts: %w", err)
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("fonts: parse %s: %w", file, err)
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}
