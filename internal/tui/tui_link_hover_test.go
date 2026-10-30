package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// cell is a visible character of a rendered line, with the OSC 8 sequence
// that opened the hyperlink it is in ("" for none) and whether it is
// underlined.
type cell struct {
	r         rune
	link      string
	underline bool
}

// lineState is what a line of rendered output shows: its cells, the OSC 8
// opening sequences in it, in order, and the hyperlink and underline it
// leaves open at its end. Each line starts with neither, as a viewport
// that shows it alone would.
type lineState struct {
	cells           []cell
	opens           []string
	openAtEnd       string
	underlineAtEnd  bool
	unknownSequence bool
}

func scanLine(line string) lineState {
	var st lineState
	link, ul := "", false
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			if m := osc8RE.FindString(line[i:]); m != "" && strings.HasPrefix(line[i:], m) {
				if m == linkEnd {
					link = ""
				} else {
					link = m
					st.opens = append(st.opens, m)
				}
				i += len(m)
				continue
			}
			if m := sgrRE.FindString(line[i:]); m != "" {
				ul = applySGR(ul, m[2:len(m)-1])
				i += len(m)
				continue
			}
			st.unknownSequence = true
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		st.cells = append(st.cells, cell{r, link, ul})
		i += size
	}
	st.openAtEnd, st.underlineAtEnd = link, ul
	return st
}

// applySGR is the underline state after SGR parameters params, skipping
// the colour arguments of 38, 48 and 58 so "38;5;4" is not an underline.
func applySGR(ul bool, params string) bool {
	if params == "" {
		return false
	}
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		if strings.Contains(p, ":") { // "4:3" and the like
			p, _, _ = strings.Cut(p, ":")
			if p == "4" {
				ul = true
			}
			continue
		}
		n, _ := strconv.Atoi(p)
		switch n {
		case 0, 24:
			ul = false
		case 4:
			ul = true
		case 38, 48, 58:
			if i+1 < len(ps) && ps[i+1] == "5" {
				i += 2
			} else if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			}
		}
	}
	return ul
}

// linkText is the text of the cells in link across lines, each line's
// trimmed and joined by a space.
func linkText(lines []lineState, link string) string {
	var parts []string
	for _, l := range lines {
		var b strings.Builder
		for _, c := range l.cells {
			if c.link == link {
				b.WriteRune(c.r)
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// checkLinkLines checks every line of out: no line leaves a hyperlink or
// an underline open, every cell in a hyperlink is underlined and no cell
// outside one is.
func checkLinkLines(t *testing.T, out string) []lineState {
	t.Helper()
	var states []lineState
	for n, line := range strings.Split(out, "\n") {
		st := scanLine(line)
		if st.unknownSequence {
			t.Errorf("line %d: unexpected escape sequence: %q", n, line)
		}
		if st.openAtEnd != "" || st.underlineAtEnd {
			t.Errorf("line %d leaves a link (%q) or underline (%v) open: %q", n, st.openAtEnd, st.underlineAtEnd, line)
		}
		for _, c := range st.cells {
			if c.link != "" && !c.underline {
				t.Errorf("line %d: %q in a link is not underlined: %q", n, c.r, line)
			}
			if c.link == "" && c.underline {
				t.Errorf("line %d: %q outside a link is underlined: %q", n, c.r, line)
			}
		}
		states = append(states, st)
	}
	return states
}

// A multi-word title on one line is one hyperlink: opened once, closed
// once, with the spaces between its words inside it and underlined, so
// WezTerm, which groups by the opening sequence, highlights all of it.
func TestLinkTitleOnOneLineIsOneSpan(t *testing.T) {
	const title = "Big news about the thing"
	for _, theme := range []string{themeDark, themeLight, "notty"} {
		for _, md := range []string{
			"### Head\nBody [" + title + "](https://a.example/x) end.",
			"### Head [" + title + "](https://a.example/x) more",
			"- item [" + title + "](https://a.example/x)",
			"**Sources**\n- [" + title + "](https://a.example/x)",
		} {
			out := Render(md, 100, theme)
			states := checkLinkLines(t, out)
			var opens []string
			for _, st := range states {
				opens = append(opens, st.opens...)
			}
			if len(opens) != 1 || strings.Count(out, linkEnd) != 1 {
				t.Errorf("%s %q: %d opens and %d closes, want 1 of each:\n%q", theme, md, len(opens), strings.Count(out, linkEnd), out)
				continue
			}
			if got := linkText(states, opens[0]); got != title {
				t.Errorf("%s %q: link text %q, want %q", theme, md, got, title)
			}
			for _, st := range states {
				var in strings.Builder
				for _, c := range st.cells {
					if c.link != "" {
						in.WriteRune(c.r)
					}
				}
				if s := in.String(); s != "" && s != title {
					t.Errorf("%s %q: the span holds %q, want the whole title with its spaces", theme, md, s)
				}
			}
		}
	}
}

// A title word-wrapped across k lines opens the link once on each, with
// the same id and URI, and each line closes it.
func TestLinkTitleWordWrappedOpensOncePerLine(t *testing.T) {
	const title = "Another quite long title written here so that it wraps over several narrow lines"
	for _, theme := range []string{themeDark, themeLight} {
		for _, width := range []int{24, 30, 40, 60} {
			out := Render("Body text ["+title+"](https://b.example/y) end.", width, theme)
			states := checkLinkLines(t, out)
			k, opens := 0, map[string]bool{}
			for _, st := range states {
				has := false
				for _, c := range st.cells {
					has = has || c.link != ""
				}
				if has {
					k++
				}
				if has && len(st.opens) != 1 {
					t.Errorf("%s width %d: a line opens the link %d times", theme, width, len(st.opens))
				}
				for _, o := range st.opens {
					opens[o] = true
				}
			}
			if k < 2 || strings.Count(out, "\x1b]8;id=") != k || len(opens) != 1 {
				t.Errorf("%s width %d: %d lines, %d opens, %d distinct:\n%q", theme, width, k, strings.Count(out, "\x1b]8;id="), len(opens), out)
				continue
			}
			for o := range opens {
				if !strings.Contains(o, "https://b.example/y") {
					t.Errorf("open %q", o)
				}
				if got := linkText(states, o); got != title {
					t.Errorf("%s width %d: link text %q, want %q", theme, width, got, title)
				}
			}
		}
	}
}

// A link hard-wrapped across k lines, its tokens merged into one span
// first, reopens once per line with the same sequence, underlined, and
// each line closes it.
func TestLinkHardWrappedOpensOncePerLine(t *testing.T) {
	tok := func(k int, text string) string {
		s := string(tokStart) + strconv.Itoa(k) + string(tokEnd)
		return s + strings.Repeat(string(tokPad), max(0, utf8.RuneCountInString(text)-utf8.RuneCountInString(s)))
	}
	words := []string{"alphabet", "betamax", "gammaray"}
	var ls links
	var toks []string
	for i, w := range words {
		ls = append(ls, link{token: tok(i, w), url: "https://h.example/z", text: w, id: 7})
		toks = append(toks, ls[i].token)
	}
	in := "x \x1b[1m" + strings.Join(toks, " \x1b[0m\x1b[38;5;252m ") + "\x1b[0m end"
	out := fitLines(ls.restore(in), 8)
	states := checkLinkLines(t, out)
	k := 0
	for n, st := range states {
		has := false
		for _, c := range st.cells {
			has = has || c.link != ""
		}
		if !has {
			continue
		}
		k++
		if len(st.opens) != 1 || st.opens[0] != linkStart("https://h.example/z", 7) {
			t.Errorf("line %d opens %q", n, st.opens)
		}
	}
	if k < 3 || strings.Count(out, "\x1b]8;id=") != k {
		t.Errorf("%d lines hold the link, %d opens:\n%q", k, strings.Count(out, "\x1b]8;id="), out)
	}
	if got := strings.Join(strings.Fields(linkText(states, linkStart("https://h.example/z", 7))), ""); got != strings.Join(words, "") {
		t.Errorf("link text %q", got)
	}
}

// Adjacent links to different places never merge, with or without a
// space between them.
func TestAdjacentLinksKeepSeparateSpans(t *testing.T) {
	for _, theme := range []string{themeDark, themeLight, "notty"} {
		for _, sep := range []string{"", " ", " and "} {
			md := "Body [First link here](https://a.example/1)" + sep + "[Second one there](https://b.example/2) end."
			out := Render(md, 100, theme)
			states := checkLinkLines(t, out)
			var opens []string
			for _, st := range states {
				opens = append(opens, st.opens...)
			}
			if len(opens) != 2 || opens[0] == opens[1] || strings.Count(out, linkEnd) != 2 {
				t.Errorf("%s sep %q: opens %q, %d closes", theme, sep, opens, strings.Count(out, linkEnd))
				continue
			}
			if a, b := linkText(states, opens[0]), linkText(states, opens[1]); a != "First link here" || b != "Second one there" {
				t.Errorf("%s sep %q: spans %q and %q", theme, sep, a, b)
			}
		}
	}
}

// The same title linked twice, to the same URL, stays two links: tokens
// merge only within one link.
func TestSameURLTwiceStaysTwoSpans(t *testing.T) {
	out := Render("Body [Same title words](https://a.example/1) [Same title words](https://a.example/1) end.", 100, themeDark)
	checkLinkLines(t, out)
	if n := strings.Count(out, "\x1b]8;id="); n != 2 {
		t.Errorf("%d opens, want 2:\n%q", n, out)
	}
}

// restore leaves no underline or hyperlink open after a link whose tokens
// are separated by SGR resets, and the text after it is plain.
func TestLinkGapStylingDoesNotLeak(t *testing.T) {
	ls := links{
		{token: string(tokStart) + "0" + string(tokEnd) + string(tokPad), url: "https://a.example/", text: "one2", id: 0},
		{token: string(tokStart) + "1" + string(tokEnd) + string(tokPad), url: "https://a.example/", text: "two2", id: 0},
	}
	in := "\x1b[38;5;252mpre " + ls[0].token + "\x1b[0m \x1b[24m\x1b[38;5;4m " + ls[1].token + "\x1b[0m\x1b[38;5;252m after"
	out := ls.restore(in)
	states := checkLinkLines(t, out)
	if strings.Count(out, "\x1b]8;id=") != 1 || linkText(states, linkStart("https://a.example/", 0)) != "one2  two2" {
		t.Errorf("%q", out)
	}
}
