package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// escapeIssues scans rendered output for anything a terminal could act on
// beyond SGR/CSI styling and well-formed OSC 8 hyperlinks: other OSC
// sequences, BEL, control bytes inside an OSC 8 URL, and OSC 8
// links left open at the end of a line (a viewport cuts between lines).
func escapeIssues(out string) []string {
	var issues []string
	for n, line := range strings.Split(out, "\n") {
		open := false
		for i := 0; i < len(line); {
			c := line[i]
			switch {
			case c == 0x1b && i+1 < len(line) && line[i+1] == '[':
				j := i + 2
				for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
					j++
				}
				i = j + 1
				continue
			case c == 0x1b && i+1 < len(line) && line[i+1] == ']':
				end := strings.Index(line[i:], "\x1b\\")
				if end < 0 {
					issues = append(issues, "unterminated OSC on line "+fmt.Sprint(n)+": "+fmt.Sprintf("%+q", line[i:]))
					return issues
				}
				seq := line[i+2 : i+end]
				if !strings.HasPrefix(seq, "8;") {
					issues = append(issues, "non-OSC-8 sequence "+fmt.Sprintf("%+q", seq))
				} else {
					parts := strings.SplitN(seq, ";", 3)
					if len(parts) != 3 {
						issues = append(issues, "malformed OSC 8 "+fmt.Sprintf("%+q", seq))
					} else {
						for _, b := range []byte(parts[2]) {
							if b < 0x20 || b == 0x7f {
								issues = append(issues, "control byte in OSC 8 URL "+fmt.Sprintf("%+q", parts[2]))
								break
							}
						}
						if parts[2] == "" {
							open = false
						} else {
							open = true
						}
					}
				}
				i += end + 2
				continue
			case c == 0x1b:
				issues = append(issues, "stray ESC on line "+fmt.Sprint(n)+": "+fmt.Sprintf("%+q", line[i:min(len(line), i+20)]))
			case c == 0x07 || (c < 0x20 && c != '\t'):
				issues = append(issues, "control byte "+fmt.Sprintf("%+q", string(c))+" on line "+fmt.Sprint(n))
			}
			// C1 controls (U+0080-U+009F) pass through too; UTF-8
			// terminals do not act on them, so they are not flagged.
			_, size := utf8.DecodeRuneInString(line[i:])
			i += size
		}
		if open {
			issues = append(issues, "OSC 8 link left open at the end of line "+fmt.Sprint(n)+": "+fmt.Sprintf("%+q", ansi.Strip(line)))
		}
	}
	return issues
}

// checkHostile asserts the invariants every rendering must keep, whatever
// the Markdown: no escape injection, no visible URL scheme or placeholder,
// every line within width and OSC 8 balanced per line.
func checkHostile(t *testing.T, label, out string, width int) {
	t.Helper()
	plain := ansi.Strip(out)
	for _, bad := range []string{"http", "QZX"} {
		if strings.Contains(plain, bad) {
			t.Errorf("%s: visible %q:\n%s", label, bad, plain)
		}
	}
	for _, issue := range escapeIssues(out) {
		t.Errorf("%s: %s", label, issue)
	}
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("%s: line %d is %d cells, over %d: %q", label, i, w, width, ansi.Strip(l))
		}
	}
}

var hostileWidths = []int{30, 31, 33, 40, 47, 59, 80, 97, 120, 163, 200}

// renderHostile renders md at every width and theme and checks it.
func renderHostile(t *testing.T, label, md string) {
	t.Helper()
	for _, theme := range []string{"dark", "light", "notty"} {
		for _, w := range hostileWidths {
			checkHostile(t, label+"/"+theme+"/"+fmt.Sprint(w), Render(md, w, theme), w)
		}
	}
}

var hostileLinkCases = map[string]string{
	"bel-in-url":      "### Bel link\nBody [click here now](https://evil.com/a\x07b) end.",
	"c1-controls":     "### C1\nBody [source](https://a.com/\u009b31m) end \u009d8;;x\u009c.",
	"paren-url":       "### Paren\nBody [source](https://en.wikipedia.org/wiki/Go_(lang)) end.",
	"space-url":       "### Space\nBody [source](<https://a.com/x y>) end.",
	"long-url":        "### Long\nBody [source](https://example.com/" + strings.Repeat("segment/", 120) + "?q=" + strings.Repeat("x", 300) + ") end.",
	"long-host":       "### Host\nSee https://" + strings.Repeat("sub", 40) + ".example.com/x now.",
	"idn":             "### IDN\n[source](https://bücher.example.de/x) [source](https://xn--bcher-kva.example/) [source](https://例え.テスト/パス)",
	"wide-title":      "### Wide\n[日本語のとても長いリンクのタイトルです日本語のとても長いリンクのタイトルです](https://example.jp/x)",
	"nested":          "### Nested\nBody [[inner](https://in.com)](https://out.com) and [a](https://a.com)[b](https://b.com).",
	"adjacent":        "### Adjacent\n[source](https://a.com)[source](https://b.com)[source](https://c.com) https://d.com/xhttps://e.com/y",
	"code-block":      "### Code\nSee:\n\n```\ncurl https://block.com/very/long/path/that/goes/on/and/on/and/on/forever/and/ever/and/ever/and/ever\n```\n",
	"bare":            "### Bare\nSee https://bare.com/x, and <https://auto.com/y>. Also (https://paren.com/z) and \"https://q.com\".",
	"schemes":         "### Schemes\n[source](javascript:alert(1)) [f](file:///etc/passwd) [d](data:text/html,x)",
	"deep-list":       "### Deep\n- a\n  - b\n    - c\n      - d\n        - e https://averyveryveryverylongsubdomainname.example-domain-name.com/x\n          - f [a long linked title here](https://x.com)",
	"quote-link":      "### Quote\n> A quoted note with [a link title that wraps around](https://q.example/" + strings.Repeat("p", 80) + ") and https://bare.example/x",
	"many-links":      "### Many\n" + strings.Repeat("[source](https://m.example/x) ", 40),
	"sources-list":    "### Item\nBody.\n\n**Sources**\n\n- [Title with \x1b[31mred\x1b[0m](https://s.example/a)\n- [https://s.example/b](https://s.example/b)\n- [](https://s.example/c)",
	"link-in-heading": "### Heading with [a link](https://h.example/x) and https://h2.example\nBody.",
}

// Hostile link content never injects escapes, shows a URL or a placeholder,
// leaves a hyperlink open at a line end, or overflows a width of 30-200.
func TestAdversarialRenderHostileLinks(t *testing.T) {
	for name, md := range hostileLinkCases {
		renderHostile(t, name, md)
	}
}

// renderedLinks lists the OSC 8 hyperlinks in out as url -> visible text.
func renderedLinks(out string) map[string]string {
	links := map[string]string{}
	for _, part := range strings.Split(out, "\x1b]8;id=")[1:] {
		_, rest, _ := strings.Cut(part, ";")
		url, rest, _ := strings.Cut(rest, "\x1b\\")
		text, _, _ := strings.Cut(rest, "\x1b]8;;")
		links[url] += ansi.Strip(text)
	}
	return links
}

// A link whose URL holds control characters (here an OSC 8 that would
// link the rest of the line to http://x) shows neither the URL nor its
// escapes: they are stripped before rendering.
func TestAdv_RenderControlCharsInURLNeverReachTerminal(t *testing.T) {
	md := "### Esc link\nBody [source](https://evil.com/\x1b]8;;http://x\x07a) end."
	for _, w := range []int{40, 80} {
		out := Render(md, w, "notty")
		checkHostile(t, fmt.Sprint("esc-in-url/", w), out, w)
	}
}

// Escape sequences in a digest's text or a link's title, e.g. OSC 52
// writing the clipboard or CSI 2J clearing the screen, never reach the
// terminal.
func TestAdv_RenderStripsEscapesFromText(t *testing.T) {
	for _, md := range []string{
		"### Body inj\nPlain \x1b]52;c;ZXZpbA==\x07 text and \x1b[2J clear.",
		"### Title inj\nBody [\x1b[2J\x1b[8mhidden title words\x07](https://ok.com) end.",
	} {
		out := Render(md, 80, "notty")
		if strings.Contains(out, "\x1b]52") || strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[8m") || strings.Contains(out, "\x07") {
			t.Errorf("raw escapes from the digest reach the terminal: %q", ansi.Strip(out))
		}
	}
}

// A URL with balanced parentheses, as Wikipedia's often have, links in
// full, with no stray ")" shown.
func TestAdv_RenderURLWithBalancedParentheses(t *testing.T) {
	const url = "https://en.wikipedia.org/wiki/Go_(programming_language)"
	out := Render("### Go\nBody [source]("+url+") end.", 80, "notty")
	plain := ansi.Strip(out)
	if _, ok := renderedLinks(out)[url]; !ok || strings.Contains(plain, ")") {
		t.Errorf("links %q, visible:\n%s", renderedLinks(out), plain)
	}
}

// A bare URL in a code span ends at the closing backtick, so the code
// span closes and the hyperlink's URL has no "`".
func TestAdv_RenderBareURLInCodeSpanKeepsBackticks(t *testing.T) {
	out := Render("### Code\nRun `https://code.example/x` now.", 80, "notty")
	plain := ansi.Strip(out)
	links := renderedLinks(out)
	if strings.Contains(plain, "`") || len(links) != 1 {
		t.Errorf("visible:\n%s\nlinks %q", plain, links)
	}
	for u := range links {
		if u != "https://code.example/x" {
			t.Errorf("hyperlink to %q", u)
		}
	}
}

// Text that looks like a placeholder is shown as it is: placeholders are
// private-use runes stripped from the Markdown, and a quote's must be a
// whole line.
func TestAdv_RenderTextLikeOldPlaceholders(t *testing.T) {
	md := "### Tok\nEncoded as QZXB0Q in the payload.\n\n> quoted note"
	plain := ansi.Strip(Render(md, 80, "notty"))
	if !strings.Contains(plain, "Encoded as") || strings.Count(plain, "quoted note") != 1 {
		t.Errorf("quote token in text:\n%s", plain)
	}
	md = "### Tok\nThe string QZX0Q appears in [logs](https://a.example)."
	out := Render(md, 80, "notty")
	if !strings.Contains(ansi.Strip(out), "QZX0Q appears") || strings.Count(out, "\x1b]8;id=") != 1 {
		t.Errorf("link token in text: %q", ansi.Strip(out))
	}
}

// A hyperlink hard-wrapped across lines is closed at the end of each line
// and reopened on the next.
func TestAdv_FitLinesKeepsOSC8Balanced(t *testing.T) {
	out := fitLines(" x "+hyperlink("https://a.example/x", strings.Repeat("w", 50), 0)+" end", 12)
	if issues := escapeIssues(out); len(issues) > 0 || strings.Count(out, "\n") < 4 {
		t.Errorf("%q: %v", out, issues)
	}
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 12 {
			t.Errorf("line %d is %d cells", i, w)
		}
		if i > 0 && strings.Contains(ansi.Strip(l), "w") && !strings.HasPrefix(l, "\x1b]8;id=db0;https://a.example/x") {
			t.Errorf("line %d does not reopen the link: %q", i, l)
		}
	}
}
