package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// gotenbergItem is the item that rendered with "off" alone on a line in a
// 150-column terminal: glamour's paragraph wrap did not count the
// hyphens in "self-hosted" and "document-to-PDF", ran the line three cells
// past the width, and its document margin wrapped it again.
const gotenbergItem = "### Gotenberg RCE exploited to drop reverse shells\n\n`ACT`\n\n" +
	"Attackers are hitting unpatched self-hosted instances of this popular Docker document-to-PDF API. " +
	"Upgrade and keep it off the internet. Source is a news roundup. " +
	"[source](https://thehackernews.com/2026/10/threatsday-critical-firewall-flaws.html)\n"

// hyphenItem has a hyphen in most words, and a heading that wraps.
const hyphenItem = "### Follow-up: long-awaited zero-day fix for self-hosted, open-source, end-to-end-encrypted chat\n\n" +
	"A run-of-the-mill state-of-the-art follow-up: the long-term, high-severity, pre-auth, zero-click " +
	"remote-code-execution bug in self-hosted, open-source chat servers is fixed in 3.2-rc1; " +
	"well-known, battle-tested work-arounds were short-lived. Re-run up-to-date scans on all third-party " +
	"add-ons. [source](https://example.com/a-b-c)\n"

// greedy wraps words at spaces, as many to a line as fit in width cells.
func greedy(words []string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range words {
		switch {
		case cur == "":
			cur = w
		case ansi.StringWidth(cur)+1+ansi.StringWidth(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// block is the run of non-blank plain lines of out from the one that
// starts with first, trimmed.
func block(out, first string) []string {
	var got []string
	for _, l := range strings.Split(ansi.Strip(out), "\n") {
		t := strings.TrimSpace(l)
		if len(got) == 0 && !strings.HasPrefix(t, first) {
			continue
		}
		if t == "" {
			break
		}
		got = append(got, strings.Join(strings.Fields(t), " "))
	}
	return got
}

// words is the words of lines, a "↗ domain" link counting as one.
func words(lines []string) []string {
	var ws []string
	for _, w := range strings.Fields(strings.Join(lines, " ")) {
		if n := len(ws); n > 0 && ws[n-1] == "↗" {
			ws[n-1] += " " + w
			continue
		}
		ws = append(ws, w)
	}
	return ws
}

// checkNaturalWrap: the block that starts with first breaks where wrapping
// its words at the text width (width less the document margins) does, so
// no word is left alone on a line the sentence carries on after.
func checkNaturalWrap(t *testing.T, label, out, first string, width int) {
	t.Helper()
	got := block(out, first)
	if len(got) == 0 {
		t.Fatalf("%s: no block starting %q:\n%s", label, first, ansi.Strip(out))
	}
	for i, l := range got[:len(got)-1] {
		if !strings.Contains(l, " ") && len(l) <= 4 {
			t.Errorf("%s: orphan %q on line %d of %q", label, l, i, got)
		}
	}
	want := greedy(words(got), width-2*docMargin)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s: lines break as\n%s\nwant\n%s", label, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > width {
			t.Errorf("%s: line %d is %d cells, over %d", label, i, w, width)
		}
	}
}

func TestParagraphsWrapNaturally(t *testing.T) {
	m, _ := richModel(t, 150, 40, themeDark)
	tuiWidth := m.viewport.Width // the content pane of a 150-column terminal
	for _, theme := range []string{themeDark, themeLight, "notty"} {
		for width := 60; width <= 130; width++ {
			label := theme + "/" + strconv.Itoa(width)
			out := Render(gotenbergItem, width, theme)
			checkNaturalWrap(t, "gotenberg "+label, out, "Attackers", width)
			if theme != "notty" {
				checkLinkLines(t, out)
			}
			out = Render(hyphenItem, width, theme)
			checkNaturalWrap(t, "hyphens "+label, out, "A run-of-the-mill", width)
			checkNaturalWrap(t, "heading "+label, out, "Follow-up:", width)
			if !strings.Contains(ansi.Strip(out), "end-to-end-encrypted") || strings.ContainsRune(out, hyphenMark) {
				t.Errorf("hyphens %s: hyphens not restored:\n%s", label, ansi.Strip(out))
			}
		}
		got := block(Render(gotenbergItem, tuiWidth, theme), "Attackers")
		if len(got) != 2 || !strings.HasPrefix(got[1], "off the internet.") {
			t.Errorf("%s at the 150-column pane width %d: %q", theme, tuiWidth, got)
		}
	}
}

// Hyphens that are Markdown, or in code, stay hyphens to glamour.
func TestProtectHyphensKeepsSyntax(t *testing.T) {
	h := string(hyphenMark)
	for in, want := range map[string]string{
		"- a-b":                     "- a" + h + "b",
		"  > - x-y":                 "  > - x" + h + "y",
		"---":                       "---",
		"- - -":                     "- - -",
		"| a-b | c |":               "| a" + h + "b | c |",
		"|---|:--:|":                "|---|:--:|",
		"| --- | --- |":             "| --- | --- |",
		"see www.a-b.com, a-b":      "see www.a-b.com, a" + h + "b",
		"mail a-b@x.org":            "mail a-b@x.org",
		"<!-- x-y -->":              "<!-- x-y -->",
		"```\na-b\n```\nc-d":        "```\na-b\n```\nc" + h + "d",
		"~~~go\nx := a-b\n~~~\ne-f": "~~~go\nx := a-b\n~~~\ne" + h + "f",
		"`--force` and a -- b":      "`--force` and a -- b",
		"`a-b` code":                "`a" + h + "b` code",
		"[see](/a-b) x-y":           "[see](/a-b) x" + h + "y",
		"[r-1]: /ref-path":          "[r-1]: /ref-path",
		"[a-b][r-1] and [c-d]":      "[a-b][r-1] and [c-d]",
		"[part](#section-id)":       "[part](#section-id)",
		"path /usr/x-y and a#b-c":   "path /usr/x-y and a#b-c",
		`foo\-bar \- x`:             `foo\-bar \- x`,
		"_Reuters_-reported":        "_Reuters_-reported",
		"- - nested-item":           "- - nested" + h + "item",
		"1. - nested2-item":         "1. - nested2" + h + "item",
		"3.2-rc1 a-":                "3.2" + h + "rc1 a-",
		"x long-hyphenated-word-xx": "x long-hyphenated-word-xx", // wider than the limit
	} {
		if got := protectHyphens(in, 20); got != want {
			t.Errorf("protectHyphens(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitise("a" + string(hyphenMark) + "b"); got != "ab" {
		t.Errorf("sanitise kept hyphenMark: %q", got)
	}
}

// Each case rendered as it was before hyphens were protected (review
// findings): no mark leaks into a URL glamour shows, escapes, emphasis
// and nested list markers stay Markdown, and a word wider than the line
// still wraps at its hyphens, inside the margin.
func TestProtectHyphensRegressions(t *testing.T) {
	plain := func(md string, width int, theme string) (string, []string) {
		out := Render(md, width, theme)
		if strings.ContainsRune(out, hyphenMark) || strings.Contains(out, "%EE%80%85") {
			t.Errorf("%q: hyphenMark leaks: %q", md, out)
		}
		var lines []string
		for _, l := range strings.Split(ansi.Strip(out), "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, strings.TrimRight(l, " "))
			}
		}
		return out, lines
	}
	for _, theme := range []string{themeDark, "notty"} {
		// 1: link destinations, references and anchors.
		for md, want := range map[string]string{
			"Read [see](/a-b) now.":                    " Read see now.",
			"Read [r-1][r-1] now.\n\n[r-1]: /ref-path": " Read r-1 /ref-path now.",
			"Go to [part](#section-id).":               " Go to part.",
		} {
			if _, got := plain(md, 80, theme); strings.Join(got, "\n") != want {
				t.Errorf("%s %q: %q, want %q", theme, md, got, want)
			}
		}
		// 2: escaped hyphens.
		for md, want := range map[string]string{
			`foo\-bar and baz`: " foo-bar and baz",
			`\- not a list`:    " - not a list",
		} {
			if _, got := plain(md, 80, theme); strings.Join(got, "\n") != want {
				t.Errorf("%s %q: %q, want %q", theme, md, got, want)
			}
		}
		// 3: a hyphenated word longer than the line.
		_, got := plain("x supercalifragilistic-expialidocious-hyphenated-word-long y", 30, theme)
		want := []string{" x supercalifragilistic-", " expialidocious-hyphenated-", " word-", " long y"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s long word: %q, want %q", theme, got, want)
		}
		// 5: nested list markers.
		_, got = plain("- - nested-item\n\n1. - nested2-item", 80, theme)
		if len(got) != 4 || strings.TrimSpace(got[0]) != "•" || strings.TrimSpace(got[1]) != "• nested-item" ||
			strings.TrimSpace(got[2]) != "1." || strings.TrimSpace(got[3]) != "• nested2-item" {
			t.Errorf("%s nested lists: %q", theme, got)
		}
	}
	// 4: emphasis next to a hyphen.
	out, got := plain("_Reuters_-reported news", 80, themeDark)
	if !regexp.MustCompile(`\x1b\[[0-9;]*;3mReuters`).MatchString(out) || strings.Join(got, "") != " Reuters-reported news" {
		t.Errorf("emphasis lost: %q", out)
	}
	if _, got := plain("_Reuters_-reported news", 80, "notty"); strings.Join(got, "") != " *Reuters*-reported news" {
		t.Errorf("notty emphasis lost: %q", got)
	}
}
