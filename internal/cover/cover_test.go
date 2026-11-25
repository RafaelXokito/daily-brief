package cover

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.png")

var goldenContent = Content{
	Issue:  323,
	Date:   time.Date(2026, time.November, 19, 6, 0, 0, 0, time.UTC),
	Topics: []string{"Java", "Security", "AI", "Tech"},
	Lead:   "Apache Roller RCE exploited in the wild",
}

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return img
}

func TestRenderSize(t *testing.T) {
	b, err := Render(goldenContent)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decode(t, b)
	if got := img.Bounds(); got != image.Rect(0, 0, 1264, 1680) {
		t.Fatalf("bounds %v, want 1264x1680", got)
	}
	r, g, bl, _ := img.At(10, 10).RGBA()
	got := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 0xff}
	if want := (color.RGBA{0xef, 0xe6, 0xd2, 0xff}); got != want {
		t.Errorf("background at (10,10) = %v, want %v", got, want)
	}
}

func TestRenderGolden(t *testing.T) {
	b, err := Render(goldenContent)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	path := filepath.Join("testdata", "golden.png")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	got, exp := decode(t, b), decode(t, want)
	if got.Bounds() != exp.Bounds() {
		t.Fatalf("bounds %v, golden %v", got.Bounds(), exp.Bounds())
	}
	diff, total := 0, 0
	for y := got.Bounds().Min.Y; y < got.Bounds().Max.Y; y++ {
		for x := got.Bounds().Min.X; x < got.Bounds().Max.X; x++ {
			total++
			if !sameColor(got.At(x, y), exp.At(x, y)) {
				diff++
			}
		}
	}
	frac := float64(diff) / float64(total)
	if frac >= 0.005 {
		t.Errorf("%d of %d pixels (%.2f%%) differ from the golden, want under 0.5%%", diff, total, frac*100)
	}
	// The render is pure Go and deterministic; the tight bound only leaves
	// room for float differences across CPU architectures, and catches a
	// 1px shift of any text line or ornament.
	if frac >= 0.0001 {
		t.Errorf("%d of %d pixels (%.4f%%) differ from the golden, want under 0.01%%", diff, total, frac*100)
	}
}

// sameColor allows one step of rounding per channel.
func sameColor(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	near := func(x, y uint32) bool {
		x, y = x>>8, y>>8
		return x-y <= 1 || y-x <= 1
	}
	return near(ar, br) && near(ag, bg) && near(ab, bb)
}

func TestRenderDiffersByContent(t *testing.T) {
	a, err := Render(goldenContent)
	if err != nil {
		t.Fatal(err)
	}
	other := goldenContent
	other.Lead = "A different lead line entirely"
	b, err := Render(other)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("a different lead rendered the same image")
	}
}

func TestFitLeadShort(t *testing.T) {
	size, lines := fit(t, "Short")
	if size != 66 || len(lines) != 1 || lines[0] != "Short" {
		t.Errorf("fitLead(Short) = %v, %q, want 66, [Short]", size, lines)
	}
}

func TestFitLeadWraps(t *testing.T) {
	lead := "Twenty words make a lead line long enough to wrap across the cover more than once in the italic size"
	if n := len(strings.Fields(lead)); n != 20 {
		t.Fatalf("fixture has %d words, want 20", n)
	}
	size, lines := fit(t, lead)
	if len(lines) < 2 || len(lines) > 3 {
		t.Errorf("got %d lines, want 2 or 3: %q", len(lines), lines)
	}
	if size > 66 || size < 44 {
		t.Errorf("size %v, want within 44..66", size)
	}
	if got := strings.Join(lines, " "); got != lead {
		t.Errorf("lines rejoin to %q, want the whole lead", got)
	}
	assertFits(t, size, lines, leadWidth)
}

func TestFitLeadShrinks(t *testing.T) {
	lead := strings.Repeat("several words that take room ", 5)
	size, lines := fit(t, lead)
	if size >= 66 || size < 44 {
		t.Errorf("size %v, want shrunk below 66 and at least 44", size)
	}
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	if len(lines) > 3 || strings.HasSuffix(lines[len(lines)-1], "...") {
		t.Errorf("want at most 3 untruncated lines, got %q", lines)
	}
	assertFits(t, size, lines, leadWidth)
}

func TestFitLeadTruncates(t *testing.T) {
	lead := strings.TrimSpace(strings.Repeat("word ", 200))
	size, lines := fit(t, lead)
	if size != 44 {
		t.Errorf("size %v, want 44", size)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if !strings.HasSuffix(lines[2], "...") {
		t.Errorf("last line %q does not end with ...", lines[2])
	}
	assertFits(t, size, lines, leadWidth)
}

func assertFits(t *testing.T, size float64, lines []string, width int) {
	t.Helper()
	f := mustFace(t, "fell-italic", size)
	for _, l := range lines {
		if w := measure(f, l, 0); w > width {
			t.Errorf("line %q is %d px wide, want at most %d", l, w, width)
		}
	}
}

func TestRoman(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "I"}, {4, "IV"}, {9, "IX"}, {14, "XIV"}, {40, "XL"}, {90, "XC"},
		{400, "CD"}, {1999, "MCMXCIX"}, {2026, "MMXXVI"}, {3888, "MMMDCCCLXXXVIII"},
	}
	for _, tt := range tests {
		if got := roman(tt.n); got != tt.want {
			t.Errorf("roman(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestTopicsLine(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"Java"}, "Java"},
		{[]string{"Java", "Security", "AI", "Tech"}, "Java · Security · AI · Tech"},
		{[]string{"A", "B", "C", "D", "E", "F"}, "A · B · C · D"},
		{[]string{"A", " ", "B"}, "A · B"},
	}
	for _, tt := range tests {
		if got := topicsLine(tt.in); got != tt.want {
			t.Errorf("topicsLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func fit(t *testing.T, lead string) (float64, []string) {
	t.Helper()
	size, lines, err := fitLead(lead, leadWidth)
	if err != nil {
		t.Fatalf("fitLead: %v", err)
	}
	return size, lines
}

func mustFace(t *testing.T, name string, size float64) font.Face {
	t.Helper()
	f, err := face(name, size)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A lead that takes four lines at 46px but three at 44px must be set at
// 44px whole, not truncated.
func TestFitLeadTriesTheSmallestSize(t *testing.T) {
	at46, at44 := mustFace(t, "fell-italic", 46), mustFace(t, "fell-italic", leadMin)
	lead := ""
	for n := 1; n < 200; n++ {
		candidate := strings.TrimSpace(strings.Repeat("a word ", n))
		if len(wrap(at46, candidate, 0, leadWidth)) == 4 && len(wrap(at44, candidate, 0, leadWidth)) == 3 {
			lead = candidate
			break
		}
	}
	if lead == "" {
		t.Fatal("no fixture needs four lines at 46px and three at 44px")
	}
	size, lines := fit(t, lead)
	if size != leadMin || len(lines) != 3 || strings.Join(lines, " ") != lead {
		t.Errorf("fitLead = %v, %q, want 44 and the whole lead in three lines", size, lines)
	}
}

func TestTitleLines(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{"The Daily", "Brief"}},
		{"The Daily Brief", []string{"The Daily", "Brief"}},
		{"Gazette", []string{"Gazette"}},
		{"Le Brief Quotidien", []string{"Le Brief", "Quotidien"}},
		{"  The\tMorning\x00 Post ", []string{"The Morning", "Post"}},
		{"Morning Post", []string{"Morning", "Post"}},
	}
	for _, tt := range tests {
		if got := titleLines(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("titleLines(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFitTitle(t *testing.T) {
	tests := []struct {
		title   string
		maxSize float64
	}{
		{"", nameSize},
		{"The Daily Brief", nameSize},
		{"The Extraordinarily Long Morning Chronicle of Absolutely Everything", nameSize - 1},
		{"Supercalifragilisticexpialidocious", nameSize - 1},
	}
	for _, tt := range tests {
		size, lines, err := fitTitle(tt.title)
		if err != nil {
			t.Fatalf("fitTitle(%q): %v", tt.title, err)
		}
		if size > tt.maxSize || size <= 0 {
			t.Errorf("fitTitle(%q) size %v, want positive and at most %v", tt.title, size, tt.maxSize)
		}
		f := mustFace(t, "fraktur", size)
		for _, l := range lines {
			if w := measure(f, l, 0); w > contentRight-contentLeft {
				t.Errorf("fitTitle(%q): line %q is %d px, want at most %d", tt.title, l, w, contentRight-contentLeft)
			}
		}
	}
}

func TestRenderDefaultTitleMatchesEmpty(t *testing.T) {
	a, err := Render(goldenContent)
	if err != nil {
		t.Fatal(err)
	}
	named := goldenContent
	named.Title = "The Daily Brief"
	b, err := Render(named)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error(`Title "The Daily Brief" renders differently from an empty Title`)
	}
}
