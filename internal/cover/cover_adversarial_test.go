package cover

import (
	"strings"
	"testing"
	"time"
)

// The lead and topics come from the editor model and from feeds, so the
// cover must render whatever they hold without panicking or overflowing.
func TestRenderAdversarialContent(t *testing.T) {
	long := strings.Repeat("x", 400)
	tests := []struct {
		name string
		c    Content
	}{
		{"empty", Content{}},
		{"zero issue and date", Content{Lead: "Lead"}},
		{"control characters", Content{Issue: 1, Date: goldenContent.Date, Lead: "a\x00b\x1b[31m\tc\r\nd", Topics: []string{"\x07Java\n"}}},
		{"unbroken lead", Content{Issue: 1, Date: goldenContent.Date, Lead: long}},
		{"huge topics", Content{Issue: 1, Date: goldenContent.Date, Lead: "Lead", Topics: []string{long, long, long, long, long}}},
		{"glyphs the fonts lack", Content{Issue: 1, Date: goldenContent.Date, Lead: "日本語 🚀 ❦ ⁂", Topics: []string{"🚀"}}},
		{"far future", Content{Issue: 1 << 30, Date: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), Lead: "Lead"}},
		{"markup", Content{Issue: 1, Date: goldenContent.Date, Lead: "<script>alert(1)</script> & </p>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := Render(tt.c)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if img := decode(t, b); img.Bounds().Dx() != 1264 || img.Bounds().Dy() != 1680 {
				t.Errorf("bounds %v, want 1264x1680", img.Bounds())
			}
		})
	}
}

func TestFitLeadBreaksUnbrokenWords(t *testing.T) {
	size, lines := fitLead(strings.Repeat("W", 300), leadWidth)
	if len(lines) == 0 || len(lines) > 3 {
		t.Fatalf("got %d lines, want 1 to 3", len(lines))
	}
	assertFits(t, size, lines, leadWidth)
}

func TestFitLeadDropsControlCharacters(t *testing.T) {
	_, lines := fitLead("a\x00b\nc\td\x1b", leadWidth)
	if got := strings.Join(lines, " "); got != "ab c d" {
		t.Errorf("lines %q, want [ab c d]", got)
	}
}

func TestFitLeadEmpty(t *testing.T) {
	if size, lines := fitLead("  \n ", leadWidth); size != 66 || len(lines) != 0 {
		t.Errorf("fitLead(blank) = %v, %q, want 66 and no lines", size, lines)
	}
}

// Long topics must not squeeze the fixed right-hand label into an
// ellipsis: it keeps room for two whole lines.
func TestMetaColumnsKeepEditionWhole(t *testing.T) {
	meta := newStyle("fell-sc", metaSize, metaSpacing)
	long := strings.Repeat("Artificial Intelligence ", 10)
	for _, left := range []string{"", "Java", long} {
		lw, rw := meta.columns(left, "Edition of the Morning")
		if lw+rw > contentRight-contentLeft-metaGap {
			t.Errorf("left %q: columns %d + %d overflow the row", left, lw, rw)
		}
		if lines := wrap(meta.f, "Edition of the Morning", meta.spacing, rw); len(lines) > metaMaxLines {
			t.Errorf("left %q: edition wraps to %q in %d px, want at most two lines", left, lines, rw)
		}
	}
}
