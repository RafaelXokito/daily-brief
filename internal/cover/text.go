package cover

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// style is a face with CSS letter-spacing, which Chrome adds after every
// glyph, the last one included.
type style struct {
	f         font.Face
	spacing   fixed.Int26_6
	asc, desc float64 // as Chrome rounds them, unhinted
	normal    float64 // line-height:normal
}

// metricsSize is large enough that the hinted metrics of a face at this
// size, scaled down, are the font's unhinted design metrics.
const metricsSize = 2048

func newStyle(name string, size, spacingEm float64) style {
	m := face(name, metricsSize).Metrics()
	k := size / metricsSize / 64
	asc := math.Round(float64(m.Ascent) * k)
	desc := math.Round(float64(m.Descent) * k)
	gap := math.Round(float64(m.Height-m.Ascent-m.Descent) * k)
	return style{
		f:       face(name, size),
		spacing: fixed.Int26_6(math.Round(spacingEm * size * 64)),
		asc:     asc,
		desc:    desc,
		normal:  asc + desc + max(gap, 0),
	}
}

// lineHeight is CSS line-height:normal: ascent, descent and line gap.
func (s style) lineHeight() float64 {
	return s.normal
}

// baseline places the baseline in a line box of height lh starting at
// top, splitting the leading evenly above and below as CSS does.
func (s style) baseline(top, lh float64) float64 {
	return math.Round(top + (lh-s.asc-s.desc)/2 + s.asc)
}

// centered draws text centred in a line-height:normal box at top and
// returns the box's bottom.
func (s style) centered(img draw.Image, text string, top float64, col color.Color) float64 {
	lh := s.lineHeight()
	s.centeredLine(img, text, top, lh, col)
	return top + lh
}

func (s style) centeredLine(img draw.Image, text string, top, lh float64, col color.Color) {
	x := centerX - float64(advance(s.f, text, s.spacing))/128
	s.draw(img, x, s.baseline(top, lh), text, col)
}

// row lays out a .meta flex row: left and right columns separated by the
// gap, both shrunk in proportion to their natural widths when they do not
// fit, each wrapped to at most two lines, the right one right-aligned. It
// returns the row's bottom.
func (s style) row(img draw.Image, left, right string, top float64, rightCol color.Color) float64 {
	lw, rw := s.columns(left, right)
	ll := truncate(s.f, wrap(s.f, left, s.spacing, lw), metaMaxLines, s.spacing, lw)
	rl := truncate(s.f, wrap(s.f, right, s.spacing, rw), metaMaxLines, s.spacing, rw)
	lh := s.lineHeight()
	for i, line := range ll {
		s.draw(img, contentLeft, s.baseline(top+float64(i)*lh, lh), line, ink)
	}
	for i, line := range rl {
		x := contentRight - float64(advance(s.f, line, s.spacing))/64
		s.draw(img, x, s.baseline(top+float64(i)*lh, lh), line, rightCol)
	}
	return top + float64(max(len(ll), len(rl)))*lh
}

// columns splits the row between the two texts as flex-shrink does, but
// the right text, a fixed label, always keeps room for two whole lines, so
// long topics cannot squeeze it into an ellipsis.
func (s style) columns(left, right string) (lw, rw int) {
	avail := contentRight - contentLeft - metaGap
	lw, rw = shareWidth(measure(s.f, left, s.spacing), measure(s.f, right, s.spacing), avail)
	if need := min(s.twoLineWidth(right), avail); rw < need {
		lw, rw = avail-need, need
	}
	return lw, rw
}

// twoLineWidth is the narrowest width that sets text in at most two
// lines, breaking at spaces.
func (s style) twoLineWidth(text string) int {
	words := strings.Fields(text)
	best := measure(s.f, text, s.spacing)
	for i := 1; i < len(words); i++ {
		first := measure(s.f, strings.Join(words[:i], " "), s.spacing)
		rest := measure(s.f, strings.Join(words[i:], " "), s.spacing)
		best = min(best, max(first, rest))
	}
	return best
}

// shareWidth is flex-shrink:1 for two items: when their natural widths a
// and b exceed avail, each gives up room in proportion to its width.
func shareWidth(a, b, avail int) (int, int) {
	if a+b <= avail {
		return a, b
	}
	wa := a - (a+b-avail)*a/(a+b)
	return wa, avail - wa
}

// draw sets text with its left edge at x on the baseline, kerned and
// letter-spaced.
func (s style) draw(img draw.Image, x, baseline float64, text string, col color.Color) {
	src := image.NewUniform(col)
	dot := fixed.Point26_6{X: fixed.Int26_6(math.Round(x * 64)), Y: fixed.I(int(baseline))}
	prev := rune(-1)
	for _, r := range text {
		if prev >= 0 {
			dot.X += s.f.Kern(prev, r)
		}
		dr, mask, maskp, adv, ok := s.f.Glyph(dot, r)
		if ok {
			draw.DrawMask(img, dr, src, image.Point{}, mask, maskp, draw.Over)
		}
		dot.X += adv + s.spacing
		prev = r
	}
}

// advance is the width of text, kerned and letter-spaced.
func advance(f font.Face, text string, spacing fixed.Int26_6) fixed.Int26_6 {
	var w fixed.Int26_6
	prev := rune(-1)
	for _, r := range text {
		if prev >= 0 {
			w += f.Kern(prev, r)
		}
		a, _ := f.GlyphAdvance(r)
		w += a + spacing
		prev = r
	}
	return w
}

// measure is advance rounded up to whole pixels.
func measure(f font.Face, text string, spacing fixed.Int26_6) int {
	return advance(f, text, spacing).Ceil()
}

// wrap breaks text into lines no wider than width, at spaces, and inside
// a word only when the word alone is too wide.
func wrap(f font.Face, text string, spacing fixed.Int26_6, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		for _, piece := range breakWord(f, word, spacing, width) {
			switch {
			case line == "":
				line = piece
			case measure(f, line+" "+piece, spacing) <= width:
				line += " " + piece
			default:
				lines = append(lines, line)
				line = piece
			}
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// breakWord splits a word wider than width into pieces that fit, at
// least one character each.
func breakWord(f font.Face, word string, spacing fixed.Int26_6, width int) []string {
	if measure(f, word, spacing) <= width {
		return []string{word}
	}
	var pieces []string
	piece := ""
	for _, r := range word {
		if piece != "" && measure(f, piece+string(r), spacing) > width {
			pieces = append(pieces, piece)
			piece = ""
		}
		piece += string(r)
	}
	return append(pieces, piece)
}

// truncate keeps the first n lines and, when it dropped any, ends the
// last kept line with "...", shortening it until it fits.
func truncate(f font.Face, lines []string, n int, spacing fixed.Int26_6, width int) []string {
	if len(lines) <= n {
		return lines
	}
	out := slices.Clone(lines[:n])
	last := out[n-1]
	for last != "" && measure(f, last+ellipsis, spacing) > width {
		if i := strings.LastIndexByte(last, ' '); i >= 0 {
			last = last[:i]
		} else {
			r := []rune(last)
			last = string(r[:len(r)-1])
		}
	}
	out[n-1] = last + ellipsis
	return out
}
