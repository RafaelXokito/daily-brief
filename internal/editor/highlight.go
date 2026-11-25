package editor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxHighlights     = 2  // per story
	maxHighlightWords = 12 // per highlight
)

// parseMarks strips the model's ==phrase== marks from paras and returns
// the plain paragraphs, trimmed and without empty ones, with the first
// limit valid marks as highlights. Delimiters are found by splitMarks;
// any other '=' is text. A paragraph with an odd number of
// delimiters keeps none of its marks. A mark is valid when its phrase is
// not empty, neither starts nor ends with a space, has at most
// maxHighlightWords words and no angle bracket. Whatever is dropped keeps
// its text: only the delimiters go.
func parseMarks(paras []string, limit int) ([]string, []Highlight) {
	var out []string
	var hs []Highlight
	for _, p := range paras {
		parts := splitMarks(p)
		balanced := len(parts)%2 == 1
		var b strings.Builder
		var marks []Highlight
		for i, part := range parts {
			if balanced && i%2 == 1 && len(hs)+len(marks) < limit && validMark(part) {
				marks = append(marks, Highlight{Para: len(out), Start: b.Len(), End: b.Len() + len(part)})
			}
			b.WriteString(part)
		}
		plain := b.String()
		trimmed := strings.TrimSpace(plain)
		if trimmed == "" {
			continue
		}
		// A valid mark has no edge spaces, so it lies inside the trimmed text.
		shift := len(plain) - len(strings.TrimLeftFunc(plain, unicode.IsSpace))
		for _, m := range marks {
			hs = append(hs, Highlight{Para: m.Para, Start: m.Start - shift, End: m.End - shift})
		}
		out = append(out, trimmed)
	}
	return out, hs
}

// splitMarks cuts p at its mark delimiters: runs of exactly two '=' that
// do not have a space, or the end of p, on both sides. So "x == y" and
// "===" stay text.
func splitMarks(p string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(p); {
		if p[i] != '=' {
			i++
			continue
		}
		j := i
		for j < len(p) && p[j] == '=' {
			j++
		}
		if j-i == 2 && !(spaceBefore(p, i) && spaceAfter(p, j)) {
			parts = append(parts, p[start:i])
			start = j
		}
		i = j
	}
	return append(parts, p[start:])
}

func spaceBefore(p string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(p[:i])
	return i == 0 || unicode.IsSpace(r)
}

func spaceAfter(p string, j int) bool {
	r, _ := utf8.DecodeRuneInString(p[j:])
	return j == len(p) || unicode.IsSpace(r)
}

func validMark(s string) bool {
	if s == "" || strings.ContainsAny(s, "<>") {
		return false
	}
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last) && len(strings.Fields(s)) <= maxHighlightWords
}

// unmark is s without its == marks: headlines, standfirsts and datelines
// carry no highlights.
func unmark(s string) string {
	paras, _ := parseMarks([]string{s}, 0)
	if len(paras) == 0 {
		return ""
	}
	return paras[0]
}
