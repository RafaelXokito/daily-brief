// Package cover renders the paper's cover: an old framed title page with
// the newspaper masthead, the day's topics and the editor's lead line, as
// a 1264x1680 PNG drawn with the embedded fonts (no browser needed).
//
// The layout follows docs/superpowers/specs/kobo-paper-assets/cover.tmpl.html,
// reproducing its CSS box model by hand: the constants below name the CSS
// values they stand for.
package cover

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"time"
	"unicode"

	"golang.org/x/image/font"

	"github.com/RafaelXokito/daily-brief/internal/fonts"
)

// Content is what changes from one day's cover to the next.
type Content struct {
	Title  string // the nameplate; empty means "The Daily Brief"
	Issue  int
	Date   time.Time
	Topics []string // at most four are drawn
	Lead   string
}

// Identity colours.
var (
	paper = color.RGBA{0xef, 0xe6, 0xd2, 0xff}
	ink   = color.RGBA{0x2a, 0x23, 0x18, 0xff}
	green = color.RGBA{0x2f, 0x5a, 0x3a, 0xff}
	red   = color.RGBA{0x8a, 0x1c, 0x1c, 0xff}
)

const (
	width, height = 1264, 1680

	outerPad    = 64 // .cover padding
	frameBorder = 8  // .frame border
	innerLine   = 4  // .frame outline
	innerOffset = 28 // .frame outline-offset (inward)
	padTop      = 96 // .frame padding, top and bottom
	padSide     = 72 // .frame padding, sides

	contentLeft   = outerPad + frameBorder + padSide
	contentRight  = width - contentLeft
	contentTop    = outerPad + frameBorder + padTop
	contentBottom = height - contentTop
	centerX       = width / 2

	smSize    = 46  // .sm font-size
	smSpacing = 0.2 // .sm letter-spacing, em

	ornMargin   = 18  // .orn margin
	fleuronLine = 117 // the ❦ .orn line box, as Chrome sets the fallback glyph
	asterLine   = 128 // the ⁂ .orn line box, likewise
	mastGap     = 24  // .mast margin

	metaSize     = 40   // .meta font-size
	metaSpacing  = 0.08 // .meta letter-spacing, em
	metaGap      = 30   // .meta gap
	metaMaxLines = 2

	ruleHeight = 12 // .rule border-top: 12px double
	ruleMargin = 22 // .rule margin

	nameSize       = 178  // .name font-size
	nameLineHeight = 1.02 // .name line-height
	nameMarginTop  = 36   // .name margin-top (collapses the rule's 22)

	leadMax        = 66   // .lead font-size
	leadMin        = 44   // smallest size before truncating
	leadStep       = 4    // shrink step
	leadLineHeight = 1.25 // .lead line-height
	leadMarginTop  = 56   // .lead margin-top
	leadPadding    = 20   // .lead padding, sides
	leadMaxLines   = 3
	leadWidth      = contentRight - contentLeft - 2*leadPadding

	contentWidth = contentRight - contentLeft
	defaultTitle = "The Daily Brief"
	maxTopics    = 4
	maxRunes     = 500 // cap on any one text after cleaning
	ellipsis     = "..."
)

// Render draws the cover for c and returns it as a PNG.
func Render(c Content) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(paper), image.Point{}, draw.Src)
	drawFrame(img)

	sm, err := newStyle("fell-sc", smSize, smSpacing)
	if err != nil {
		return nil, err
	}
	meta, err := newStyle("fell-sc", metaSize, metaSpacing)
	if err != nil {
		return nil, err
	}

	y := float64(contentTop)
	y = sm.centered(img, "Being a Record of the Day", y, ink)
	y += ornMargin
	drawFleuron(img, centerX, int(y), green)
	y += fleuronLine + ornMargin + mastGap

	day := ""
	if !c.Date.IsZero() {
		day = c.Date.Format("Monday, 2 January 2006")
	}
	y = meta.row(img, fmt.Sprintf("No. %d", c.Issue), day, y, ink)
	y += ruleMargin
	drawRule(img, int(y))
	y += ruleHeight + nameMarginTop

	nameSz, nameLines, err := fitTitle(c.Title)
	if err != nil {
		return nil, err
	}
	name, err := newStyle("fraktur", nameSz, 0)
	if err != nil {
		return nil, err
	}
	for _, line := range nameLines {
		name.centeredLine(img, line, y, nameSz*nameLineHeight, ink)
		y += nameSz * nameLineHeight
	}
	y += ruleMargin
	drawRule(img, int(y))
	y += ruleHeight + ruleMargin
	y = meta.row(img, topicsLine(c.Topics), "Edition of the Morning", y, red)
	y += mastGap + leadMarginTop

	size, lines, err := fitLead(c.Lead, leadWidth)
	if err != nil {
		return nil, err
	}
	lead, err := newStyle("fell-italic", size, 0)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		lead.centeredLine(img, line, y, size*leadLineHeight, ink)
		y += size * leadLineHeight
	}

	// .bottom has margin-top:auto: it sits at the foot of the frame
	// unless the lead pushes it down.
	bottom := max(float64(contentBottom)-(2*ornMargin+asterLine+sm.lineHeight()), y)
	drawAsterism(img, centerX, int(bottom+ornMargin), green)
	sm.centered(img, roman(c.Date.Year()), bottom+2*ornMargin+asterLine, ink)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("cover: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// drawFrame draws the 8px border and the 4px inner line.
func drawFrame(img draw.Image) {
	outer := image.Rect(outerPad, outerPad, width-outerPad, height-outerPad)
	strokeRect(img, outer, frameBorder)
	strokeRect(img, outer.Inset(innerOffset-innerLine), innerLine)
}

func strokeRect(img draw.Image, r image.Rectangle, w int) {
	src := image.NewUniform(ink)
	for _, side := range []image.Rectangle{
		{r.Min, image.Pt(r.Max.X, r.Min.Y+w)},
		{image.Pt(r.Min.X, r.Max.Y-w), r.Max},
		{r.Min, image.Pt(r.Min.X+w, r.Max.Y)},
		{image.Pt(r.Max.X-w, r.Min.Y), r.Max},
	} {
		draw.Draw(img, side, src, image.Point{}, draw.Src)
	}
}

// drawRule draws a 12px double rule (two 4px lines) across the content box.
func drawRule(img draw.Image, y int) {
	src := image.NewUniform(ink)
	line := ruleHeight / 3
	draw.Draw(img, image.Rect(contentLeft, y, contentRight, y+line), src, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(contentLeft, y+2*line, contentRight, y+ruleHeight), src, image.Point{}, draw.Src)
}

// topicsLine joins the first four non-blank topics with " · ".
func topicsLine(topics []string) string {
	var kept []string
	for _, t := range topics {
		if t = clean(t); t != "" {
			kept = append(kept, t)
		}
		if len(kept) == maxTopics {
			break
		}
	}
	return strings.Join(kept, " · ")
}

// titleLines cleans the title (empty means "The Daily Brief") and
// breaks it at the space nearest its middle; one word stays one line.
func titleLines(title string) []string {
	t := clean(title)
	if t == "" {
		t = defaultTitle
	}
	runes := []rune(t)
	n, split := len(runes), -1
	for i, r := range runes {
		if r == ' ' && (split < 0 || abs(2*i-n) < abs(2*split-n)) {
			split = i
		}
	}
	if split < 0 {
		return []string{t}
	}
	return []string{string(runes[:split]), string(runes[split+1:])}
}

func abs(n int) int { return max(n, -n) }

// fitTitle sets the nameplate in Fraktur at 178px, shrinking until every
// line fits the frame's inner width.
func fitTitle(title string) (size float64, lines []string, err error) {
	lines = titleLines(title)
	size = nameSize
	for {
		f, err := face("fraktur", size)
		if err != nil {
			return 0, nil, err
		}
		widest := 0
		for _, l := range lines {
			widest = max(widest, measure(f, l, 0))
		}
		if widest <= contentWidth || size <= 1 {
			return size, lines, nil
		}
		// Width scales almost linearly with size: jump close, then step.
		size = max(1, min(size-1, math.Floor(size*contentWidth/float64(widest))))
	}
}

// fitLead sets the lead in the italic at 66px, shrinking by 4px down to
// 44px until it takes at most three lines; past that the third line ends
// with "...".
func fitLead(text string, width int) (size float64, lines []string, err error) {
	text = clean(text)
	for size = leadMax; ; size -= leadStep {
		size = max(size, leadMin)
		f, err := face("fell-italic", size)
		if err != nil {
			return 0, nil, err
		}
		lines = wrap(f, text, 0, width)
		if len(lines) <= leadMaxLines {
			return size, lines, nil
		}
		if size == leadMin {
			return size, truncate(f, lines, leadMaxLines, 0, width), nil
		}
	}
}

// clean drops control characters, collapses runs of white space and caps
// the text at maxRunes, so model or feed text cannot smuggle layout into
// the cover or make it slow.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > maxRunes {
		s = strings.TrimSpace(string(runes[:maxRunes]))
	}
	return s
}

var romanNumerals = []struct {
	value  int
	symbol string
}{
	{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"},
	{50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
}

// roman writes n in Roman numerals; n below 1 gives "".
func roman(n int) string {
	var b strings.Builder
	for _, r := range romanNumerals {
		for ; n >= r.value; n -= r.value {
			b.WriteString(r.symbol)
		}
	}
	return b.String()
}

// face returns an embedded face.
func face(name string, size float64) (font.Face, error) {
	f, err := fonts.Face(name, size)
	if err != nil {
		return nil, fmt.Errorf("cover: %w", err)
	}
	return f, nil
}
