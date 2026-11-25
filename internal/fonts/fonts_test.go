package fonts

import (
	"io/fs"
	"testing"
)

func TestFace(t *testing.T) {
	for _, name := range []string{"fell", "fell-italic", "fell-sc", "fraktur"} {
		t.Run(name, func(t *testing.T) {
			f, err := Face(name, 40)
			if err != nil {
				t.Fatalf("Face(%q): %v", name, err)
			}
			defer f.Close()
			if adv, ok := f.GlyphAdvance('A'); !ok || adv <= 0 {
				t.Errorf("Face(%q) has no glyph for 'A' (advance %v, ok %v)", name, adv, ok)
			}
			if h := f.Metrics().Height.Round(); h < 30 || h > 80 {
				t.Errorf("Face(%q, 40) height %d px, want about 40 at 72 dpi", name, h)
			}
		})
	}
}

func TestFaceRefusesBadInput(t *testing.T) {
	tests := []struct {
		name string
		size float64
	}{
		{"unknown", 40},
		{"", 40},
		{"../fonts.go", 40},
		{"fell", 0},
		{"fell", -3},
	}
	for _, tt := range tests {
		if _, err := Face(tt.name, tt.size); err == nil {
			t.Errorf("Face(%q, %v) = nil error, want an error", tt.name, tt.size)
		}
	}
}

func TestFilesHoldFontsAndLicences(t *testing.T) {
	for _, name := range []string{
		"IMFeENrm28P.ttf", "IMFeENit28P.ttf", "IMFeENsc28P.ttf", "UnifrakturMaguntia-Book.ttf",
		"OFL-IMFellEnglish.txt", "OFL-UnifrakturMaguntia.txt",
	} {
		b, err := fs.ReadFile(Files, name)
		if err != nil || len(b) == 0 {
			t.Errorf("Files lacks %s: %v", name, err)
		}
	}
}
