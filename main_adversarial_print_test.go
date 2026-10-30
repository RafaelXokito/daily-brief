package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

// printJournals have highlights, updates, an overshoot, failures and
// hostile content.
func printJournals() map[string]*brief.Journal {
	hl := []brief.Highlight{{Tier: "act", Topic: "Security", Headline: "npm token theft campaign", Text: "Rotate npm tokens.", Why: "Stolen tokens."}}
	return map[string]*brief.Journal{
		"highlights": {Date: "2026-10-29", TokenBudget: 100_000,
			Overview: brief.Section{Status: brief.StatusDone, Highlights: hl,
				Markdown: "## Today's highlights\n\n1 item · 0 updates · 1 topic · ~1 min\n\n**Act now**\n\n- Rotate npm tokens. — Stolen tokens. (Security) [source](https://github.blog/npm)"},
			Sections: []*brief.Section{{Topic: "Security", Status: brief.StatusDone, TokensUsed: 50_000,
				Markdown: "### npm token theft campaign\nRotate tokens. [source](https://github.blog/npm)"}}},
		"updates-overshoot-failures": {Date: "2026-10-29", TokenBudget: 10_000,
			Overview: brief.Section{Status: brief.StatusFailed, Error: "skipped: the run went over its token budget"},
			Sections: []*brief.Section{
				{Topic: "Java", Status: brief.StatusDone, TokensUsed: 30_000, Truncated: true,
					Markdown: "### Update: Spring Boot 4 milestone slips\nSince Mon: delayed. https://spring.io/blog/m3\n\n**Related**\n\n### Kotlin 2.3\nShort. [Kotlin blog](https://kotlinlang.org/x)",
					Sources:  []brief.Source{{Title: "Spring", URL: "https://spring.io/blog/m3"}}},
				{Topic: "AI", Status: brief.StatusFailed, Error: "rate limited"},
				{Topic: "Security", Status: brief.StatusFailed, Error: "skipped: max_read_minutes leaves no room for this topic"},
			}},
		"tagged": {Date: "2026-10-29", TokenBudget: 100_000,
			Overview: brief.Section{Status: brief.StatusDone, Highlights: hl,
				Markdown: "## Today's highlights\n\n2 items · 1 to act · 0 updates · 1 topic · ~1 min"},
			Sections: []*brief.Section{{Topic: "Security", Status: brief.StatusDone, TokensUsed: 50_000,
				Markdown: "### npm token theft campaign\n`ACT` CVE-2026-1234 · CVSS 9.8 · KEV due Nov 10 · fixed in 3.2.1\nRotate tokens. [source](https://github.blog/npm)\n\n### `WATCH` Tag in a heading\n**[FYI]** x"}}},
		"hostile": {Date: "2026-10-29",
			Overview: brief.Section{Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- QZX0Q QZXB0Q [x](javascript:alert(1))"},
			Sections: []*brief.Section{{Topic: "T", Status: brief.StatusDone,
				Markdown: "### Go (lang)\n[source](https://en.wikipedia.org/wiki/Go_(lang)) `https://code.example` > not a quote\n\n> quote [a](https://q.example)"}}},
	}
}

// Piped, -print writes the journal's Markdown byte for byte, with none of
// the display rewriting (markers, placeholders, escapes).
func TestAdv_PrintPipedIsRawMarkdown(t *testing.T) {
	for name, j := range printJournals() {
		got := printOutput(j, false, 80, "dark")
		if got != j.Markdown() {
			t.Errorf("%s: piped output differs:\n%q\n%q", name, got, j.Markdown())
		}
		for _, bad := range []string{"\x1b", "↻", "↗", "QZXq", "QZX1Q"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s: piped output has %q", name, bad)
			}
		}
		for _, theme := range []string{"dark", "light", "auto", "notty", ""} {
			if printOutput(j, false, 40, theme) != got {
				t.Errorf("%s: piped output depends on theme %q", name, theme)
			}
		}
	}
	// The overshoot shows as the Markdown's warning, as it did before.
	if md := printJournals()["updates-overshoot-failures"].Markdown(); !strings.HasPrefix(md, "# Daily Brief — 2026-10-29\n\n> **Warning:** This run went over") ||
		!strings.Contains(md, "### Update: Spring Boot 4") || !strings.Contains(md, "> **Error:** rate limited") {
		t.Errorf("markdown:\n%s", md)
	}
}

// printJournal itself, with stdout a pipe, writes exactly the Markdown.
func TestAdv_PrintJournalToPipe(t *testing.T) {
	for name, j := range printJournals() {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stdout
		os.Stdout = w
		perr := printJournal(nil, j, "dark")
		os.Stdout = old
		w.Close()
		got, _ := io.ReadAll(r)
		if perr != nil || string(got) != j.Markdown() {
			t.Errorf("%s: err %v, output:\n%q", name, perr, got)
		}
	}
}

// On a terminal the same journals render within the width with no
// visible URL or placeholder.
func TestAdv_PrintTerminalJournalsFit(t *testing.T) {
	for name, j := range printJournals() {
		for _, width := range []int{30, 45, 80, 133, 200} {
			got := printOutput(j, true, width, "dark")
			plain := ansi.Strip(got)
			// (The hostile journal's text has literal "QZX…" in it.)
			if (name != "hostile" && strings.Contains(plain, "QZX")) || strings.Contains(plain, "## ") {
				t.Errorf("%s %d: visible marker or token:\n%s", name, width, plain)
			}
			if strings.Contains(plain, "http") || strings.ContainsAny(plain, "\uE000\uE001\uE002\uE003\uE004") {
				t.Errorf("%s %d: visible URL:\n%s", name, width, plain)
			}
			for _, l := range strings.Split(got, "\n") {
				if lipgloss.Width(l) > width {
					t.Errorf("%s %d: line too wide: %q", name, width, ansi.Strip(l))
				}
			}
		}
	}
}
