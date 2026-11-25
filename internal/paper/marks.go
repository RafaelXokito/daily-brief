package paper

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RafaelXokito/daily-brief/internal/editor"
)

// segment is a run of a paragraph's text, a highlighted key phrase when
// Mark. The template escapes Text and wraps a Mark in <mark class="key">.
type segment struct {
	Text string
	Mark bool
}

// paragraphViews splits each paragraph into segments at its highlights,
// sanitised and trimmed; paragraphs left empty are dropped.
func paragraphViews(ps []string, hs []editor.Highlight) [][]segment {
	out := make([][]segment, 0, len(ps))
	for i, p := range ps {
		var own []editor.Highlight
		for _, h := range hs {
			if h.Para == i {
				own = append(own, h)
			}
		}
		if segs := segments(p, own); len(segs) > 0 {
			out = append(out, segs)
		}
	}
	return out
}

// segments cuts p at hs, which must be in text order and not overlap. A
// highlight that is empty, out of range, overlaps an earlier one or cuts
// a character in two is ignored, its text kept plain.
func segments(p string, hs []editor.Highlight) []segment {
	var out []segment
	pos := 0
	for _, h := range hs {
		if h.Start < pos || h.End <= h.Start || h.End > len(p) || !runeEdge(p, h.Start) || !runeEdge(p, h.End) {
			continue
		}
		out = appendSegment(out, p[pos:h.Start], false)
		out = appendSegment(out, p[h.Start:h.End], true)
		pos = h.End
	}
	out = appendSegment(out, p[pos:], false)
	return trimSegments(out)
}

// runeEdge reports whether byte i of p starts a character or ends p.
func runeEdge(p string, i int) bool { return i == len(p) || utf8.RuneStart(p[i]) }

func appendSegment(out []segment, text string, mark bool) []segment {
	if text = sanitize(text); text != "" {
		out = append(out, segment{Text: text, Mark: mark})
	}
	return out
}

// trimSegments trims the spaces at both ends of the paragraph.
func trimSegments(segs []segment) []segment {
	for len(segs) > 0 {
		segs[0].Text = strings.TrimLeftFunc(segs[0].Text, unicode.IsSpace)
		if segs[0].Text != "" {
			break
		}
		segs = segs[1:]
	}
	for len(segs) > 0 {
		last := len(segs) - 1
		segs[last].Text = strings.TrimRightFunc(segs[last].Text, unicode.IsSpace)
		if segs[last].Text != "" {
			break
		}
		segs = segs[:last]
	}
	return segs
}

// dropCapSegments is dropCap on a paragraph's first segment; a
// highlighted segment keeps its mark on what follows the initial.
func dropCapSegments(segs []segment) (initial string, rest []segment) {
	c, tail := dropCap(segs[0].Text)
	if c == "" {
		return "", segs
	}
	rest = slices.Clone(segs)
	rest[0].Text = tail
	if tail == "" {
		rest = rest[1:]
	}
	return c, rest
}
