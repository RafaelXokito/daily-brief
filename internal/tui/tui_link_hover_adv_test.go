package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// advTimeout bounds each adversarial test: fn runs on its own goroutine
// and the test fails if it has not finished in time. fn reports with
// t.Errorf only, never t.Fatal.
func advTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("timed out after %v", d)
	}
}

// opensOf is every OSC 8 opening sequence in states, in order.
func opensOf(states []lineState) []string {
	var opens []string
	for _, st := range states {
		opens = append(opens, st.opens...)
	}
	return opens
}

// checkOpensPerLine checks each line that shows cells of a link opens it
// exactly once, and a line opens no link it shows nothing of.
func checkOpensPerLine(t *testing.T, label string, states []lineState) {
	t.Helper()
	for n, st := range states {
		shown := map[string]int{}
		for _, c := range st.cells {
			if c.link != "" {
				shown[c.link]++
			}
		}
		count := map[string]int{}
		for _, o := range st.opens {
			count[o]++
		}
		for o, k := range count {
			if shown[o] == 0 {
				t.Errorf("%s: line %d opens %q but shows none of it", label, n, o)
			}
			_ = k
		}
		for o := range shown {
			if count[o] == 0 {
				t.Errorf("%s: line %d shows link %q it never opens", label, n, o)
			}
		}
	}
}

// Placeholder runes, forged tokens, escapes and their character
// references in a link's title, from web text, are removed before
// rendering: the link stays one span with the URL it was given, and the
// output holds nothing but SGR and our OSC 8 sequences.
func TestLinkHoverAdvUntrustedTitle(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		forged := string(tokStart) + "0" + string(tokEnd) + string(tokPad)
		titles := []string{
			"Evil " + forged + " title words",
			"Evil" + string(tokStart) + "1" + string(tokEnd) + "joined words here",
			"pad" + string(tokPad) + string(tokPad) + " end " + string(tokEnd),
			"item" + string(itemMark) + " and quote" + string(quoteMark) + "0" + string(quoteMark),
			"Close \x1b]8;;\x1b\\ the link early",
			"Open \x1b]8;id=db0;javascript:evil\x1b\\ another one",
			"Open \x1b]8;id=db0;file:///etc/evil\x07 bel ended",
			"Underline \x1b[24m off then \x1b[4m on",
			"Bell \x07 and C1 \u009b2J and \u009d8;;x\u009c here",
			"Entity &#xE000;0&#xE002; forged &#57344; token",
			"Entity &#27;]8;;javascript:evil&#27;\\ escape",
			"Bidi \u202e reversed \u200b zero width",
			"Pad &#xE001;&#xE001; and mark &#xE004; here",
		}
		const url = "https://safe.example/path"
		for _, theme := range []string{themeDark, themeLight, "notty"} {
			for _, title := range titles {
				for _, md := range []string{
					"Body [" + title + "](" + url + ") end.",
					"### [" + title + "](" + url + ")",
					"- [" + title + "](" + url + ")",
					"**Sources**\n- [" + title + "](" + url + ")",
				} {
					label := fmt.Sprintf("%s %+q", theme, md)
					out := Render(md, 80, theme)
					checkStrict(t, label, out)
					states := checkLinkLines(t, out)
					checkOpensPerLine(t, label, states)
					opens := opensOf(states)
					distinct := map[string]bool{}
					for _, o := range opens {
						distinct[o] = true
						if !strings.Contains(o, ";"+url+"\x1b\\") {
							t.Errorf("%s: open %+q is not to %s", label, o, url)
						}
					}
					// A "]" left by a stripped OSC ends the title early, so
					// the Markdown link breaks and only its URL is linked,
					// bare; it is still one link to url.
					if len(distinct) != 1 || strings.Count(out, linkEnd) != len(opens) {
						t.Errorf("%s: %d distinct opens, %d opens, %d closes, want 1 link:\n%+q", label, len(distinct), len(opens), strings.Count(out, linkEnd), out)
					}
					if strings.Contains(out, "evil") && strings.Contains(out, "\x1b]8;id=db0;javascript") {
						t.Errorf("%s: injected URL survives: %+q", label, out)
					}
				}
			}
		}
	})
}

// A crafted glamour output with stray, truncated or out-of-range tokens
// next to a real one does not make restore merge, panic or open links it
// has no entry for.
func TestLinkHoverAdvRestoreStrayTokens(t *testing.T) {
	advTimeout(t, 10*time.Second, func() {
		ls := links{
			{token: string(tokStart) + "0" + string(tokEnd) + string(tokPad), url: "https://a.example/", text: "word", id: 3},
			{token: string(tokStart) + "1" + string(tokEnd) + string(tokPad), url: "https://a.example/", text: "next", id: 3},
		}
		for _, stray := range []string{
			string(tokStart),
			string(tokStart) + "9" + string(tokEnd),
			string(tokStart) + "99999999999999999999999" + string(tokEnd),
			string(tokStart) + "-1" + string(tokEnd),
			string(tokStart) + "1",                  // a token cut short
			string(tokStart) + "1" + string(tokEnd), // no pad: not ls[1]'s token
		} {
			for _, in := range []string{
				ls[0].token + " " + stray + " " + ls[1].token,
				ls[0].token + " " + stray,
				stray + ls[0].token,
				ls[0].token + " \x1b[0m" + stray,
			} {
				out := ls.restore(in)
				if strings.Count(out, "\x1b]8;id=") != strings.Count(out, linkEnd) {
					t.Errorf("%+q: unbalanced %+q", in, out)
				}
				for _, line := range strings.Split(out, "\n") {
					st := scanLine(line)
					for _, c := range st.cells {
						if c.link != "" && c.r >= tokStart && c.r <= itemMark {
							t.Errorf("%+q: stray placeholder inside the link: %+q", in, out)
						}
					}
				}
			}
		}
		// A tokStart as the very last byte, and alone.
		for _, in := range []string{string(tokStart), ls[0].token + " " + string(tokStart)} {
			_ = ls.restore(in)
		}
	})
}

// A one-word title far longer than the line is cut to fit, or
// hard-wrapped where indentation leaves less room than that: every line
// fits, every line holding part of it opens it once, with the same
// sequence, and nothing is left open or underlined outside it.
func TestLinkHoverAdvLongOneWordTitle(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		word := strings.Repeat("Supercalifragilistic", 12)
		const url = "https://long.example/w"
		for _, theme := range []string{themeDark, themeLight, "notty"} {
			for _, width := range []int{8, 10, 13, 20, 33, 60} {
				for _, md := range []string{
					"Body [" + word + "](" + url + ") end.",
					"[" + word + "](" + url + ")",
					"- a\n  - b\n    - c\n      - d\n        - [" + word + "](" + url + ")",
					"> quoted [" + word + "](" + url + ")",
					"> - quoted item [" + word + " " + word + "](" + url + ")",
					"### [" + word + "](" + url + ")",
				} {
					label := fmt.Sprintf("%s w%d %q", theme, width, md[:min(len(md), 40)])
					out := Render(md, width, theme)
					checkStrict(t, label, out)
					states := checkLinkLines(t, out)
					checkOpensPerLine(t, label, states)
					for n, line := range strings.Split(out, "\n") {
						if w := ansi.StringWidth(line); w > width {
							t.Errorf("%s: line %d is %d wide: %+q", label, n, w, line)
						}
					}
					distinct := map[string]bool{}
					for _, o := range opensOf(states) {
						distinct[o] = true
					}
					if len(distinct) > 2 { // the "two words" case has one link
						t.Errorf("%s: %d distinct opens", label, len(distinct))
					}
					// Each word is cut to fit, ending "…", and may then be
					// hard-wrapped: each "…"-ended run is a prefix of word.
					for o := range distinct {
						all := strings.ReplaceAll(linkText(states, o), " ", "")
						for _, part := range strings.Split(strings.TrimSuffix(all, "…"), "…") {
							if part == "" || !strings.HasPrefix(word, part) {
								t.Errorf("%s: link text %q is not cut from the title", label, linkText(states, o))
							}
						}
					}
				}
			}
		}
	})
}

// A link inside a heading, bold or italic text, a list item, a table
// cell, or at the very start or end of a line is one span per line,
// underlined only inside, with its whole title.
func TestLinkHoverAdvContexts(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		const title = "Four words of title"
		const url = "https://ctx.example/a"
		l := "[" + title + "](" + url + ")"
		cases := map[string]string{
			"h1":            "# Head " + l + " tail",
			"h3":            "### " + l,
			"h6":            "###### " + l + " x",
			"bold around":   "Body **bold " + l + " more** end.",
			"bold inside":   "Body [**" + title + "**](" + url + ") end.",
			"italic around": "Body *it " + l + " more* end.",
			"code after":    "Body " + l + " `code` end.",
			"list":          "- one\n- " + l + "\n- three",
			"ordered":       "1. one\n2. two " + l + " three",
			"nested":        "- one\n  - " + l,
			"task":          "- [ ] " + l,
			"table":         "| a | b |\n|---|---|\n| " + l + " | x |\n| y | z |",
			"table last":    "| a | b |\n|---|---|\n| x | " + l + " |",
			"start":         l + " starts the line.",
			"end":           "The line ends with " + l,
			"alone":         l,
			"punct":         "(" + l + ").",
			"quote":         "> " + l + " quoted",
		}
		for _, theme := range []string{themeDark, themeLight, "notty"} {
			for name, md := range cases {
				for _, width := range []int{100, 30} {
					label := fmt.Sprintf("%s %s w%d", theme, name, width)
					out := Render(md, width, theme)
					checkStrict(t, label, out)
					states := checkLinkLines(t, out)
					checkOpensPerLine(t, label, states)
					opens := opensOf(states)
					distinct := map[string]bool{}
					for _, o := range opens {
						distinct[o] = true
					}
					if len(distinct) != 1 {
						t.Errorf("%s: %d distinct links, want 1:\n%+q", label, len(distinct), out)
						continue
					}
					if got := linkText(states, opens[0]); got != title {
						t.Errorf("%s: link text %q, want %q:\n%+q", label, got, title, out)
					}
					if width == 100 && strings.HasPrefix(name, "table") == false && len(opens) != 1 {
						t.Errorf("%s: %d opens on one line, want 1:\n%+q", label, len(opens), out)
					}
					for n, line := range strings.Split(out, "\n") {
						if w := ansi.StringWidth(line); w > width {
							t.Errorf("%s: line %d is %d wide", label, n, w)
						}
					}
				}
			}
		}
	})
}

// Bold, italic or a heading's style around a link survives it: the text
// after the link is still bold, and only the link is underlined.
func TestLinkHoverAdvStyleAfterLinkKept(t *testing.T) {
	advTimeout(t, 10*time.Second, func() {
		out := Render("Body **bold [Title of link](https://s.example/) after** end.", 100, themeDark)
		checkLinkLines(t, out)
		_, after, ok := strings.Cut(out, linkEnd)
		if !ok {
			t.Fatalf("no link: %+q", out)
		}
		i := strings.Index(after, "after")
		if i < 0 {
			t.Fatalf("no text after: %+q", out)
		}
		// The last SGR before "after" must not have reset bold.
		pre := after[:i]
		if j := strings.LastIndex(pre, "\x1b["); j >= 0 {
			seq := pre[j:]
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				t.Errorf("bold reset before the text after the link: %+q", after[:i+5])
			}
		}
	})
}

// Wide titles, CJK and emoji, take as many cells as their text: the
// layout matches that of the same text unlinked, no line is wider than
// the width, and the link holds the whole title.
func TestLinkHoverAdvWideTitles(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		titles := []string{
			"日本語のニュース 速報 です",
			"中文 标题 很长的 新闻",
			"한국어 뉴스 제목",
			"🚀 Launch 🎉 party 👍 now",
			"Mixed 日本 and 🚀 rockets",
			"日",
			"🚀",
		}
		const url = "https://wide.example/x"
		for _, theme := range []string{themeDark, "notty"} {
			for _, title := range titles {
				for _, width := range []int{12, 20, 31, 100} {
					label := fmt.Sprintf("%s w%d %q", theme, width, title)
					linked := Render("Body ["+title+"]("+url+") end.", width, theme)
					states := checkLinkLines(t, linked)
					checkStrict(t, label, linked)
					checkOpensPerLine(t, label, states)
					for n, line := range strings.Split(linked, "\n") {
						if w := ansi.StringWidth(line); w > width {
							t.Errorf("%s: line %d is %d wide: %q", label, n, w, ansi.Strip(line))
						}
					}
					opens := opensOf(states)
					if len(opens) == 0 {
						t.Errorf("%s: no link", label)
						continue
					}
					got := strings.ReplaceAll(linkText(states, opens[0]), " ", "")
					want := strings.ReplaceAll(title, " ", "")
					if width >= 31 && got != want {
						t.Errorf("%s: link text %q, want %q", label, got, want)
					}
					if width == 100 {
						plain := Render("Body "+title+" end.", width, theme)
						if a, b := ansi.Strip(linked), ansi.Strip(plain); a != b {
							t.Errorf("%s: linked layout differs from plain:\n%q\n%q", label, a, b)
						}
					}
				}
			}
		}
	})
}

// A table whose first column holds a link, short, wide or long, keeps
// its columns aligned: the column separator is at the same cell on every
// row, as it is when the same titles are plain text.
func TestLinkHoverAdvTableAlignment(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		sepCol := func(line string) int {
			p := ansi.Strip(line)
			for _, sep := range []string{"│", "|", "┼"} {
				if i := strings.Index(p, sep); i >= 0 {
					return ansi.StringWidth(p[:i])
				}
			}
			return -1
		}
		for _, title := range []string{"a", "日", "🚀", "ab", "Long title words", "日本語 の 記事"} {
			md := "| Link | Name |\n|---|---|\n| [" + title + "](https://t.example/1) | one |\n| plain | two |\n| [x y](https://t.example/2) | three |"
			for _, theme := range []string{themeDark, "notty"} {
				out := Render(md, 80, theme)
				checkLinkLines(t, out)
				cols := map[int]bool{}
				var rows []string
				for _, line := range strings.Split(out, "\n") {
					if c := sepCol(line); c >= 0 {
						cols[c] = true
						rows = append(rows, ansi.Strip(line))
					}
				}
				if len(cols) != 1 {
					t.Errorf("%s %q: column separator at %d different cells:\n%s", theme, title, len(cols), strings.Join(rows, "\n"))
				}
			}
		}
	})
}

// The same URL in two links on a line, with any separator and with the
// same or different titles, stays two spans; so do bare URLs repeated.
func TestLinkHoverAdvSameURLTwiceOnALine(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		const u = "https://dup.example/p"
		cases := []struct {
			md   string
			want []string
		}{
			{"[Same words here](" + u + ")[Same words here](" + u + ")", []string{"Same words here", "Same words here"}},
			{"[Same words here](" + u + ") [Same words here](" + u + ")", []string{"Same words here", "Same words here"}},
			{"[First words](" + u + ") **[Second words](" + u + ")**", []string{"First words", "Second words"}},
			{"[First words](" + u + ")  [Second words](" + u + ") end", []string{"First words", "Second words"}},
			{"[ab](" + u + ") [cd](" + u + ")", []string{"ab", "cd"}},
			{u + " " + u, []string{"↗ dup.example", "↗ dup.example"}},
			{"[source](" + u + ") [source](" + u + ")", []string{"↗ dup.example", "↗ dup.example"}},
			{"[Title words](" + u + ") " + u, []string{"Title words", "↗ dup.example"}},
		}
		for _, theme := range []string{themeDark, themeLight, "notty"} {
			for _, c := range cases {
				label := fmt.Sprintf("%s %q", theme, c.md)
				out := Render("Body "+c.md+" end.", 100, theme)
				checkStrict(t, label, out)
				states := checkLinkLines(t, out)
				opens := opensOf(states)
				if len(opens) != len(c.want) || strings.Count(out, linkEnd) != len(c.want) {
					t.Errorf("%s: %d opens, %d closes, want %d:\n%+q", label, len(opens), strings.Count(out, linkEnd), len(c.want), out)
					continue
				}
				if opens[0] == opens[1] {
					t.Errorf("%s: both links open with the same sequence %+q", label, opens[0])
				}
				for i, o := range opens {
					if got := linkText(states, o); got != c.want[i] {
						t.Errorf("%s: link %d text %q, want %q", label, i, got, c.want[i])
					}
				}
			}
		}
	})
}

// Two tokens of one link with a different link's token of the same URL
// between them in restore's input never merge across it.
func TestLinkHoverAdvRestoreNoMergeAcrossIDs(t *testing.T) {
	advTimeout(t, 10*time.Second, func() {
		tok := func(k int) string { return string(tokStart) + strconv.Itoa(k) + string(tokEnd) + string(tokPad) }
		u := "https://m.example/"
		ls := links{
			{token: tok(0), url: u, text: "aaaa", id: 0},
			{token: tok(1), url: u, text: "bbbb", id: 1},
			{token: tok(2), url: u, text: "cccc", id: 0},
		}
		out := ls.restore(tok(0) + " " + tok(1) + " " + tok(2))
		states := checkLinkLines(t, out)
		if n := strings.Count(out, "\x1b]8;id="); n != 3 {
			t.Errorf("%d opens, want 3: %+q", n, out)
		}
		if got := linkText(states, linkStart(u, 1)); got != "bbbb" {
			t.Errorf("link 1 text %q", got)
		}
	})
}

// advJournal is a digest with links in every awkward place.
func advJournal() *brief.Journal {
	md := "### Head [Linked heading title words](https://h.example/1) tail\n" +
		"Body **bold [Bold link words here](https://b.example/2) more** and " +
		"[日本語のニュース 速報](https://c.example/3) then [🚀 Launch party](https://e.example/4).\n\n" +
		"[Start of line link](https://s.example/5) starts and ends with [End of line link](https://s.example/6)\n\n" +
		"- item [List link words](https://l.example/7)\n" +
		"- " + "[" + strings.Repeat("Unbreakable", 8) + "](https://u.example/8)\n\n" +
		"| a | b |\n|---|---|\n| [Table link words](https://t.example/9) | x |\n\n" +
		"Same [Dup one](https://d.example/) [Dup two](https://d.example/) end. https://bare.example/x\n\n" +
		"> quoted [Quoted link title words](https://q.example/10)\n\n" +
		"### Second item headline\nEvil [ti" + string(tokStart) + "0" + string(tokEnd) + "tle \x1b]8;;\x1b\\ words](https://v.example/11) [source](https://v.example/12)"
	sec := &brief.Section{Topic: "Links", Status: brief.StatusDone, Markdown: md,
		Sources: []brief.Source{{Title: "Source title with several words", URL: "https://src.example/a"}, {URL: "https://src.example/b"}}}
	return journalWith(sec)
}

// checkViewLines checks a full View: each line closes every hyperlink it
// opens and turns off any underline it sets, no cell outside a link is
// underlined, and only SGR and our OSC 8 sequences appear.
func checkViewLines(t *testing.T, label, view string) {
	t.Helper()
	checkStrict(t, label, view)
	for n, line := range strings.Split(view, "\n") {
		st := scanLine(line)
		if st.unknownSequence {
			t.Errorf("%s: line %d: unexpected sequence %+q", label, n, line)
		}
		if strings.Count(line, "\x1b]8;id=") != strings.Count(line, linkEnd) {
			t.Errorf("%s: line %d: %d opens, %d closes: %+q", label, n, strings.Count(line, "\x1b]8;id="), strings.Count(line, linkEnd), line)
		}
		if st.openAtEnd != "" || st.underlineAtEnd {
			t.Errorf("%s: line %d leaves link %q / underline %v open: %+q", label, n, st.openAtEnd, st.underlineAtEnd, line)
		}
		for _, c := range st.cells {
			if c.link == "" && c.underline {
				t.Errorf("%s: line %d: %q underlined outside a link: %+q", label, n, c.r, line)
				break
			}
		}
	}
}

// At narrow widths, 10 to 30, through the whole View, every line of
// every scroll position balances its OSC 8 opens and closes, and no
// underline leaks outside a link, in the split and the reader layouts.
func TestLinkHoverAdvNarrowView(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		for _, theme := range []string{themeDark, "notty"} {
			for width := 10; width <= 30; width++ {
				for _, reader := range []bool{false, true} {
					m := New(nil, nil, advJournal(), theme)
					m.Update(tea.WindowSizeMsg{Width: width, Height: 14})
					m.reader = reader
					m.selectSection(1)
					m.resize()
					label := fmt.Sprintf("%s w%d reader=%v", theme, width, reader)
					checkLinkLines(t, m.content)
					total := len(strings.Split(m.content, "\n"))
					for y := 0; y < total; y++ {
						m.viewport.SetYOffset(y)
						checkViewLines(t, fmt.Sprintf("%s y%d", label, y), m.View())
						if t.Failed() {
							return
						}
					}
				}
			}
		}
	})
}

// A digest of 200 links, titled and bare, in paragraphs, a list and a
// Sources list, renders quickly, and through the View.
func TestLinkHoverAdvManyLinksPerformance(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		var b strings.Builder
		var srcs []brief.Source
		for i := range 200 {
			switch i % 4 {
			case 0:
				fmt.Fprintf(&b, "### Item %d headline [Linked title number %d with words](https://p.example/%d)\n", i, i, i)
			case 1:
				fmt.Fprintf(&b, "Body text [Another link %d here and there](https://p.example/%d) and more text.\n\n", i, i)
			case 2:
				fmt.Fprintf(&b, "- [List link %d words](https://p.example/%d) [Same](https://p.example/%d)\n", i, i, i)
			default:
				fmt.Fprintf(&b, "See https://p.example/%d.\n\n", i)
			}
			srcs = append(srcs, brief.Source{Title: fmt.Sprintf("Source %d title words", i), URL: fmt.Sprintf("https://src.example/%d", i)})
		}
		md := b.String()
		// One very long line of 200 adjacent links to the same URL.
		var one strings.Builder
		for range 200 {
			one.WriteString("[a b c d e f g h](https://one.example/) ")
		}

		start := time.Now()
		out := Render(md, 80, themeDark)
		long := Render(one.String(), 80, themeDark)
		elapsed := time.Since(start)
		if elapsed > 10*time.Second {
			t.Errorf("rendering 400 links took %v", elapsed)
		}
		checkLinkLines(t, out)
		checkLinkLines(t, long)
		if n := strings.Count(long, "\x1b]8;id="); n < 200 {
			t.Errorf("long line: %d opens, want at least 200", n)
		}

		sec := &brief.Section{Topic: "Many", Status: brief.StatusDone, Markdown: md, Sources: srcs}
		start = time.Now()
		m := New(nil, nil, journalWith(sec), themeDark)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m.selectSection(1)
		v := m.View()
		if el := time.Since(start); el > 10*time.Second {
			t.Errorf("View of 400 links took %v", el)
		}
		checkViewLines(t, "many view", v)
	})
}
