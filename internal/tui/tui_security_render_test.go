package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

// allowedSeqRE matches the only escape sequences rendered output may
// hold: SGR, and OSC 8 hyperlinks as hyperlink writes them, with a URL of
// printable characters, or ending one.
var allowedSeqRE = regexp.MustCompile(`^(?:\x1b\[[0-9;:]*m|\x1b\]8;id=db[0-9]+;[!-~]+\x1b\\|\x1b\]8;;\x1b\\)`)

// strictEscapeIssues lists what in out is not text, newline, tab, SGR or
// a well-formed OSC 8 sequence: any other escape sequence, a C0 or C1
// control, DEL, an invisible character, a placeholder rune or invalid
// UTF-8.
func strictEscapeIssues(out string) []string {
	var issues []string
	for i := 0; i < len(out); {
		if out[i] == 0x1b {
			m := allowedSeqRE.FindString(out[i:])
			if m == "" {
				issues = append(issues, fmt.Sprintf("escape sequence %+q", out[i:min(len(out), i+24)]))
				i++
				continue
			}
			i += len(m)
			continue
		}
		r, size := utf8.DecodeRuneInString(out[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			issues = append(issues, fmt.Sprintf("invalid UTF-8 at %d", i))
		case unicode.IsControl(r) && r != '\n' && r != '\t', brief.IsInvisible(r), r >= tokStart && r <= itemMark:
			issues = append(issues, fmt.Sprintf("U+%04X at %d", r, i))
		}
		i += size
	}
	return issues
}

func checkStrict(t *testing.T, label, out string) {
	t.Helper()
	for _, issue := range strictEscapeIssues(out) {
		t.Errorf("%s: %s in %+q", label, issue, out)
	}
}

// entityCases are Markdown whose character references decode to
// controls, invisible characters or placeholder runes, in text, code,
// links, HTML, tables and task items.
var entityCases = map[string]string{
	"sgr-osc0":      "hello &#27;[31mRED &#x1b;]0;pwned&#7; end",
	"c1":            "C1 &#x9b;31m and &#155;2J and &#x85; nel",
	"code-span":     "`&#27;[31m` in code span",
	"code-block":    "```\n&#27;[2J\n```",
	"link":          "[click &#27;[31m](https://ex.com/&#27;)",
	"autolink":      "<https://ex.com/a&#27;b>",
	"nul-surrogate": "&#0; &#xD800; &#x110000; &#99999999999; &#0000027;[31m",
	"bidi":          "&#x202E;moc.live &#x200B;zw &#8206;&#x2066;x&#x2069; &lrm;&rlm;&ZeroWidthSpace;",
	"del":           "&#127; del &#x7f;",
	"span":          "<span>&#27;]0;title&#7;</span> html <!-- &#27; --> <b>&#x1B;[2J</b>",
	"cr-bs":         "&#13;cr &#x0d; &#8; bs &#X1B;[1A",
	"reference":     "[x][r]\n\n[r]: https://ex.com/&#27;[31m \"t&#27;\"",
	"image":         "![img &#27;](https://ex.com/i.png)",
	"table":         "| a | b |\n|---|---|\n| &#27;[31m | &#x1b;]0;x&#7; |",
	"task":          "- [ ] &#27;x task\n- [x] &#x9d;8;;x&#x9c; done",
	"placeholders":  "&#xE000;0&#xE002;&#xE001; &#57347;0&#57347; &#xE004; [a](https://a.example)",
	"named":         "tab&Tab;x line&NewLine;y amp&amp;#27;z",
}

// Character references that decode to a control character, such as
// "&#27;", never reach the terminal as one, wherever they are.
func TestEntitiesNeverDecodeToControls(t *testing.T) {
	for name, md := range entityCases {
		for _, theme := range []string{"dark", "light", "notty"} {
			for _, w := range []int{30, 80} {
				out := Render(md, w, theme)
				checkStrict(t, fmt.Sprint(name, "/", theme, "/", w), out)
				checkHostile(t, fmt.Sprint(name, "/", theme, "/", w), out, w)
			}
		}
	}
	out := Render(entityCases["sgr-osc0"], 80, "dark")
	if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\x1b]0") || strings.Contains(out, "\x07") {
		t.Errorf("entities decoded into escapes: %+q", out)
	}
}

// "&#xE000;" and its kin cannot forge a link placeholder: the one real
// link is the only one, and no placeholder rune shows.
func TestEntitiesCannotForgePlaceholders(t *testing.T) {
	out := Render(entityCases["placeholders"], 80, "dark")
	if links := renderedLinks(out); len(links) != 1 || links["https://a.example"] != "a" {
		t.Errorf("links = %q", links)
	}
	if strings.ContainsFunc(ansi.Strip(out), func(r rune) bool { return r >= tokStart && r <= itemMark }) {
		t.Errorf("placeholder rune shown: %+q", out)
	}
}

// Named references that decode to tab or newline are harmless, and
// "&amp;#27;" shows as text.
func TestNamedEntities(t *testing.T) {
	plain := ansi.Strip(Render(entityCases["named"], 80, "notty"))
	if !strings.Contains(plain, "&#27;z") {
		t.Errorf("&amp;#27; should show as text: %q", plain)
	}
}

// Over the whole hostile corpus, rendered output holds no escape
// sequence but SGR and well-formed OSC 8.
func TestHostileCorpusOnlySGRAndOSC8(t *testing.T) {
	corpus := map[string]string{
		"raw-escapes": "### Raw\nPlain \x1b]52;c;ZXZpbA==\x07 \x1b[2J \x1bc \x1bP1$q\x1b\\ \x1b]0;t\x1b\\ \u009b31m \r\b\x7f end.",
		"bidi":        "### Bidi\nText ‮evil‬ ⁦x⁩ zero​width ⁠ ؜ emoji 👩‍💻.",
	}
	for k, v := range hostileLinkCases {
		corpus[k] = v
	}
	for k, v := range entityCases {
		corpus["entity-"+k] = v
	}
	for name, md := range corpus {
		for _, theme := range []string{"dark", "light", "notty"} {
			for _, w := range hostileWidths {
				checkStrict(t, fmt.Sprint(name, "/", theme, "/", w), Render(md, w, theme))
			}
		}
	}
	// The zero-width joiner is kept.
	if out := Render(corpus["bidi"], 80, "notty"); !strings.Contains(out, "👩‍💻") {
		t.Errorf("ZWJ sequence lost: %+q", out)
	}
	// So is a whole rich journal, as the TUI renders it.
	checkStrict(t, "rich", Render(richJournal().Markdown(), 80, "dark"))
}

// termSafe keeps SGR and our OSC 8 links and removes every other
// sequence and control character, without swallowing newlines.
func TestTermSafe(t *testing.T) {
	link := linkStart("https://ok.example/x", 3) + underlineOn + "ok" + underlineOff + linkEnd
	for _, c := range []struct{ in, want string }{
		{"\x1b[1;38;5;12mbold\x1b[0m", "\x1b[1;38;5;12mbold\x1b[0m"},
		{link, link},
		{"a\x1b]0;title\x07b", "ab"},
		{"a\x1b]0;title\x1b\\b", "ab"},
		{"a\x1b]52;c;ZXZpbA==\x07b", "ab"},
		{"a\x1b]8;;javascript:x\x07b", "ab"},
		{"a\x1b]8;id=x;https://a b\x1b\\b", "ab"},
		{"a\x1b]8;x=1;https://a\x1b\\b", "ab"},
		{"a\x1b[2Jb\x1b[1Ac\x1b[?25ld", "abcd"},
		{"a\x1b[31;1 mb", "ab"},
		{"a\x1bcb\x1b7c\x1b(Bd", "abcd"},
		{"a\x1bP1$qm\x1b\\b", "ab"},
		{"a\x1b_apc\x1b\\b\x1b^pm\x07c", "abc"},
		{"a\x1b]0;open\nb", "a\nb"},
		{"a\x1b[12\nb", "a\nb"},
		{"a\x1b", "a"},
		{"a\x07b\bc\rd\x7fe", "abcde"},
		{"a\u009b31mb\u009d0;x\u009cc\u0085d", "a31mb0;xcd"},
		{"a\xffb\xc0c", "abc"},
		{"a‮b​c⁦d‍e", "abcd‍e"},
		{"x\ty\nz", "x\ty\nz"},
	} {
		if got := termSafe(c.in); got != c.want {
			t.Errorf("termSafe(%+q) = %+q, want %+q", c.in, got, c.want)
		}
	}
}

// The Overview's list goes through the same filter as rendered Markdown.
func TestOverviewListOnlySGRAndOSC8(t *testing.T) {
	sec := &brief.Section{Topic: "T‮", Status: brief.StatusDone, Markdown: "### Patch it now please\nBody. [source](https://a.example)"}
	j := oneHighlight("T‮", "Patch it now please", sec)
	j.Overview.Highlights[0].Text = "Patch \x1b]0;t\x07 now \u009b2J ‮reversed"
	j.Overview.Highlights[0].Why = "Because\r \x1b[2J"
	j.BudgetWarnings = []string{"over \x1b]52;c;eA==\x07 budget"}
	m, _ := hlModel(t, j, 100, 30)
	checkStrict(t, "overview list", m.content)
}

// A budget level from a hand-edited journal cannot write escapes into the
// header.
func TestHeaderBudgetLevelStripped(t *testing.T) {
	j := richJournal()
	j.BudgetLevel = "warn\x1b]0;pwned\x07"
	m := New(nil, nil, j, "dark")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if v := m.View(); strings.Contains(v, "\x1b]0") || strings.Contains(v, "\x07") {
		t.Errorf("header has the budget level's escapes: %+q", ansi.Strip(v))
	}
}

// The Sources list shows a lookalike host as punycode.
func TestSourcesShowPunycode(t *testing.T) {
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: "### Item\nBody.",
		Sources: []brief.Source{{Title: "Apple news", URL: "https://аpple.com/x"}}}
	plain := ansi.Strip(Render(sec.Render(), 80, "notty"))
	if !strings.Contains(plain, "Apple news · xn--pple-43d.com") || strings.Contains(plain, "аpple") {
		t.Errorf("sources line: %q", plain)
	}
	plain = ansi.Strip(Render("See [source](https://аpple.com/x).", 80, "notty"))
	if !strings.Contains(plain, "↗ xn--pple-43d.com") {
		t.Errorf("source link: %q", plain)
	}
}
